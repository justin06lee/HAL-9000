package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var errInterrupted = errors.New("interrupted")

// ── HAL Conversation ────────────────────────────────────────

type halConversation struct {
	mu        sync.Mutex
	sessionID string
	cancelMu  sync.Mutex
	cancelFn  context.CancelFunc
}

// interrupt cancels any in-flight send
func (h *halConversation) interrupt() {
	h.cancelMu.Lock()
	defer h.cancelMu.Unlock()
	if h.cancelFn != nil {
		h.cancelFn()
		h.cancelFn = nil
	}
}

func (h *halConversation) send(userMsg string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	h.cancelMu.Lock()
	h.cancelFn = cancel
	h.cancelMu.Unlock()
	defer func() {
		h.cancelMu.Lock()
		h.cancelFn = nil
		h.cancelMu.Unlock()
	}()

	var prompt string
	if h.sessionID == "" {
		// First message: include system prompt + portfolio summary only (saves tokens)
		var sb strings.Builder
		sb.WriteString(halSystemPrompt)
		portfolio := loadPortfolio()
		if portfolio != "" {
			sb.WriteString("\n\n## Project Portfolio\n")
			sb.WriteString(portfolio)
		}
		sb.WriteString("\n\nUser: ")
		sb.WriteString(userMsg)
		prompt = sb.String()
	} else {
		prompt = userMsg
	}

	// Use "--" to prevent prompt from being parsed as flags
	args := []string{"-p", "--output-format", "json"}
	if claudeModel != "" {
		args = append(args, "--model", claudeModel)
	}
	if thinkingBudget != "" {
		args = append(args, "--thinking-budget", thinkingBudget)
	}
	if h.sessionID != "" {
		args = append(args, "--resume", h.sessionID)
	}
	args = append(args, "--", prompt)

	cmd := exec.CommandContext(ctx, claudeBin, args...)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.Canceled {
			return "", errInterrupted
		}
		errDetail := strings.TrimSpace(stderr.String())
		if errDetail != "" {
			return "", fmt.Errorf("claude: %s", errDetail)
		}
		return "", fmt.Errorf("claude: %w", err)
	}

	var data struct {
		SessionID string      `json:"session_id"`
		Result    interface{} `json:"result"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return strings.TrimSpace(string(out)), nil
	}

	if data.SessionID != "" {
		h.sessionID = data.SessionID
	}

	switch v := data.Result.(type) {
	case string:
		return v, nil
	case []interface{}:
		var parts []string
		for _, block := range v {
			if m, ok := block.(map[string]interface{}); ok {
				if m["type"] == "text" {
					if text, ok := m["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n"), nil
		}
	}

	return strings.TrimSpace(string(out)), nil
}

func (h *halConversation) extractSpec(response string) string {
	start := strings.Index(response, "<spec>")
	if start < 0 {
		return ""
	}
	end := strings.Index(response, "</spec>")
	if end < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(response[start+6 : end])
}

func (h *halConversation) checkStartBuild(response string) bool {
	return strings.Contains(response, "<start_build/>") || strings.Contains(response, "<start_build />")
}

// ── Project Specs ───────────────────────────────────────────

type projectSpec struct {
	name        string
	description string
	repoPath    string
	specText    string
	filePath    string
}

func listSpecs() []projectSpec {
	var specs []projectSpec
	entries, err := os.ReadDir(specsDir)
	if err != nil {
		return specs
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		fpath := filepath.Join(specsDir, e.Name())
		data, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		text := string(data)
		name := strings.TrimSuffix(e.Name(), ".md")
		desc := ""
		repoPath := ""

		if strings.HasPrefix(text, "---") {
			end := strings.Index(text[3:], "---")
			if end > 0 {
				meta := text[3 : 3+end]
				for _, line := range strings.Split(meta, "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "name:") {
						name = strings.TrimSpace(line[5:])
					} else if strings.HasPrefix(line, "description:") {
						desc = strings.TrimSpace(line[12:])
					} else if strings.HasPrefix(line, "repo_path:") {
						rp := strings.TrimSpace(line[10:])
						// Validate repo_path is under projects dir
						if !filepath.IsAbs(rp) || strings.Contains(rp, "..") {
							rp = filepath.Join(filepath.Dir(specsDir), filepath.Base(rp))
						}
						repoPath = rp
					}
				}
				text = strings.TrimSpace(text[3+end+3:])
			}
		}

		specs = append(specs, projectSpec{
			name: name, description: desc, repoPath: repoPath,
			specText: text, filePath: fpath,
		})
	}
	return specs
}

func saveSpec(name, desc, repoPath, specText string) (string, error) {
	re := regexp.MustCompile(`[^\w\-]`)
	safeName := re.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "_")
	if safeName == "" {
		safeName = "project"
	}
	// Strip any path separators from sanitized name
	safeName = filepath.Base(safeName)

	fpath := filepath.Join(specsDir, safeName+".md")
	counter := 1
	for counter < 1000 { // prevent infinite loop on persistent stat errors
		if _, err := os.Stat(fpath); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", fmt.Errorf("checking spec file: %w", err)
		}
		fpath = filepath.Join(specsDir, fmt.Sprintf("%s_%d.md", safeName, counter))
		counter++
	}

	content := fmt.Sprintf("---\nname: %s\ndescription: %s\nrepo_path: %s\ncreated_at: %s\n---\n\n%s\n",
		name, desc, repoPath, time.Now().Format(time.RFC3339), specText)
	if err := os.WriteFile(fpath, []byte(content), 0o644); err != nil {
		return fpath, fmt.Errorf("writing spec: %w", err)
	}
	return fpath, nil
}

func buildMasterSpec(specs []projectSpec) string {
	var sb strings.Builder
	sb.WriteString("# Master Project Specification\n\n")
	for i, spec := range specs {
		fmt.Fprintf(&sb, "## Project %d: %s\n\n", i+1, spec.name)
		fmt.Fprintf(&sb, "**Repo:** %s\n\n", spec.repoPath)
		sb.WriteString(spec.specText)
		sb.WriteString("\n\n---\n\n")
	}
	return sb.String()
}
