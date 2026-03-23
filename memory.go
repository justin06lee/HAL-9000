package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ── Topic routing ───────────────────────────────────────────

var validTopics = map[string]string{
	"overview":     "overview.md",
	"architecture": "architecture.md",
	"requirements": "requirements.md",
	"tech-stack":   "tech-stack.md",
	"decisions":    "decisions.md",
	"notes":        "notes.md",
}

func sanitizeProjectName(name string) string {
	re := regexp.MustCompile(`[^a-z0-9\-]`)
	safe := re.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	if safe == "" || safe == "-" {
		safe = "project"
	}
	return filepath.Base(safe)
}

func projectMemDir(projectName string) string {
	return filepath.Join(memoryDir, sanitizeProjectName(projectName))
}

func topicFilePath(projectName, topic string) string {
	filename, ok := validTopics[strings.ToLower(strings.TrimSpace(topic))]
	if !ok {
		filename = "notes.md"
	}
	return filepath.Join(projectMemDir(projectName), filename)
}

// ── Save / Load ─────────────────────────────────────────────

func saveMemory(projectName, topic, content string) error {
	dir := projectMemDir(projectName)
	os.MkdirAll(dir, 0o755)

	fpath := topicFilePath(projectName, topic)
	entry := fmt.Sprintf("\n## %s\n%s\n", time.Now().Format(time.RFC3339), strings.TrimSpace(content))

	f, err := os.OpenFile(fpath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	info, _ := f.Stat()
	if info != nil && info.Size() == 0 {
		topicLabel := topic
		if topicLabel == "" {
			topicLabel = "notes"
		}
		fmt.Fprintf(f, "# %s — %s\n", projectName, topicLabel)
	}

	_, err = f.WriteString(entry)
	return err
}

// ── Portfolio ───────────────────────────────────────────────

func portfolioPath() string {
	return filepath.Join(memoryDir, "portfolio.md")
}

func savePortfolio(projectName, summary string) error {
	fpath := portfolioPath()
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}

	line := fmt.Sprintf("- **%s**: %s", projectName, summary)

	data, err := os.ReadFile(fpath)
	if err != nil {
		// New file
		content := "# Project Portfolio\n\n" + line + "\n"
		return os.WriteFile(fpath, []byte(content), 0o644)
	}

	// Update existing entry or append
	lines := strings.Split(string(data), "\n")
	marker := fmt.Sprintf("- **%s**:", projectName)
	found := false
	for i, l := range lines {
		if strings.HasPrefix(l, marker) {
			lines[i] = line
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, line)
	}
	return os.WriteFile(fpath, []byte(strings.Join(lines, "\n")), 0o644)
}

func loadPortfolio() string {
	data, err := os.ReadFile(portfolioPath())
	if err != nil {
		return ""
	}
	return string(data)
}

func removeFromPortfolio(projectName string) {
	data, err := os.ReadFile(portfolioPath())
	if err != nil {
		return
	}
	marker := fmt.Sprintf("- **%s**:", projectName)
	lines := strings.Split(string(data), "\n")
	var out []string
	for _, l := range lines {
		if !strings.HasPrefix(l, marker) {
			out = append(out, l)
		}
	}
	os.WriteFile(portfolioPath(), []byte(strings.Join(out, "\n")), 0o644)
}

// ── Load helpers ────────────────────────────────────────────

func loadProjectMemory(projectName, topic string) string {
	data, err := os.ReadFile(topicFilePath(projectName, topic))
	if err != nil {
		return ""
	}
	return string(data)
}

func listProjectTopics(projectName string) []string {
	dir := projectMemDir(projectName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var topics []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			topics = append(topics, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	return topics
}

func readMemoryTopic(projectName, topic string) string {
	path := filepath.Join(projectMemDir(projectName), topic+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "(could not read)"
	}
	return strings.TrimSpace(string(data))
}

// ── Delete ──────────────────────────────────────────────────

func listMemoryProjects() []string {
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func deleteMemory(projectName string) {
	os.RemoveAll(projectMemDir(projectName))
	removeFromPortfolio(projectName)
}

func deleteAllMemory() {
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		os.RemoveAll(filepath.Join(memoryDir, e.Name()))
	}
	// Also wipe the vector index
	os.Remove(indexFile)
}

// ── Tag extraction ──────────────────────────────────────────

type memoryEntry struct {
	project string
	topic   string
	content string
}

type portfolioEntry struct {
	project string
	summary string
}

var memoryTagRe = regexp.MustCompile(`<memory project="([^"]+)"(?:\s+topic="([^"]*)")?\s*>([\s\S]*?)</memory>`)
var portfolioTagRe = regexp.MustCompile(`<portfolio project="([^"]+)">([\s\S]*?)</portfolio>`)
var searchTagRe = regexp.MustCompile(`<search query="([^"]+)"\s*/?>`)
var discontinueTagRe = regexp.MustCompile(`<discontinue project="([^"]+)"\s*/>`)
var voiceTagRe = regexp.MustCompile(`<voice>([\s\S]*?)</voice>`)
var specTagRe = regexp.MustCompile(`<spec>[\s\S]*?</spec>`)
var startBuildTagRe = regexp.MustCompile(`<start_build\s*/?>`)

func extractAllMemories(text string) []memoryEntry {
	matches := memoryTagRe.FindAllStringSubmatch(text, -1)
	var entries []memoryEntry
	for _, m := range matches {
		topic := "notes"
		if len(m) > 2 && m[2] != "" {
			topic = m[2]
		}
		entries = append(entries, memoryEntry{
			project: m[1],
			topic:   topic,
			content: strings.TrimSpace(m[3]),
		})
	}
	return entries
}

func extractAllPortfolios(text string) []portfolioEntry {
	matches := portfolioTagRe.FindAllStringSubmatch(text, -1)
	var entries []portfolioEntry
	for _, m := range matches {
		entries = append(entries, portfolioEntry{
			project: m[1],
			summary: strings.TrimSpace(m[2]),
		})
	}
	return entries
}

func extractSearch(text string) string {
	m := searchTagRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func extractDiscontinue(text string) string {
	m := discontinueTagRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func stripSearchTags(text string) string {
	return searchTagRe.ReplaceAllString(text, "")
}

func extractVoice(text string) string {
	m := voiceTagRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// cleanDisplayText strips all internal tags from HAL's response for display
func cleanDisplayText(text string) string {
	text = memoryTagRe.ReplaceAllString(text, "")
	text = portfolioTagRe.ReplaceAllString(text, "")
	text = searchTagRe.ReplaceAllString(text, "")
	text = discontinueTagRe.ReplaceAllString(text, "")
	text = voiceTagRe.ReplaceAllString(text, "")
	text = specTagRe.ReplaceAllString(text, "")
	text = startBuildTagRe.ReplaceAllString(text, "")
	// Clean up excess blank lines left by removed tags
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(text)
}

// ── Migration ───────────────────────────────────────────────

func migrateMemories() {
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if e.Name() == "portfolio.md" {
			continue
		}
		// This is an old flat memory file — migrate it
		name := strings.TrimSuffix(e.Name(), ".md")
		oldPath := filepath.Join(memoryDir, e.Name())
		newDir := filepath.Join(memoryDir, name)
		os.MkdirAll(newDir, 0o755)
		newPath := filepath.Join(newDir, "notes.md")

		// Only migrate if target doesn't exist
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			data, err := os.ReadFile(oldPath)
			if err == nil {
				os.WriteFile(newPath, data, 0o644)
			}
		}
		os.Remove(oldPath)
	}
}
