package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type workerStatus int

const (
	statusPending workerStatus = iota
	statusRunning
	statusWaiting
	statusCompleted
	statusAuditing
	statusDone
	statusFailed
	statusAuditFailed
)

func (s workerStatus) String() string {
	switch s {
	case statusPending:
		return "pending"
	case statusRunning:
		return "running"
	case statusWaiting:
		return "waiting"
	case statusCompleted:
		return "completed"
	case statusAuditing:
		return "auditing"
	case statusDone:
		return "done"
	case statusFailed:
		return "failed"
	case statusAuditFailed:
		return "audit_failed"
	}
	return "unknown"
}

type workerState struct {
	mu         sync.Mutex
	name       string
	repoPath   string
	spec       string
	status     workerStatus
	sessionID  string
	errMsg     string
	question   *workerQuestion
	outputLog  []string
	isAudit    bool
	parentName string
}

type workerQuestion struct {
	workerName string
	text       string
	options    []string
}

type orchestrator struct {
	mu         sync.RWMutex
	workers    map[string]*workerState
	masterSpec string
	cancel     context.CancelFunc
	ctx        context.Context
	statusCh   chan workerStatusMsg
	outputCh   chan workerOutputMsg
	questionCh chan workerQuestionMsg
}

func newOrchestrator() *orchestrator {
	ctx, cancel := context.WithCancel(context.Background())
	return &orchestrator{
		workers:    make(map[string]*workerState),
		ctx:        ctx,
		cancel:     cancel,
		statusCh:   make(chan workerStatusMsg, 100),
		outputCh:   make(chan workerOutputMsg, 1000),
		questionCh: make(chan workerQuestionMsg, 50),
	}
}

func (o *orchestrator) shutdown() {
	o.cancel()
}

func (o *orchestrator) addWorker(name, repoPath, spec string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workers[name] = &workerState{
		name: name, repoPath: repoPath, spec: spec, status: statusPending,
	}
}

func (o *orchestrator) getWorker(name string) *workerState {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.workers[name]
}

func (o *orchestrator) startAll() {
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, w := range o.workers {
		w.mu.Lock()
		pending := w.status == statusPending
		w.mu.Unlock()
		if pending {
			go o.runWorker(w)
		}
	}
}

func (o *orchestrator) answerQuestion(workerName, answer string) {
	w := o.getWorker(workerName)
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.question == nil {
		w.mu.Unlock()
		return
	}
	w.question = nil
	w.mu.Unlock()
	go o.resumeWorker(w, answer)
}

func (o *orchestrator) setStatus(w *workerState, status workerStatus) {
	w.mu.Lock()
	w.status = status
	w.mu.Unlock()
	select {
	case o.statusCh <- workerStatusMsg{name: w.name, status: status}:
	default:
	}
}

func (o *orchestrator) emitOutput(w *workerState, text string) {
	w.mu.Lock()
	w.outputLog = append(w.outputLog, text)
	// Cap output log to prevent unbounded growth
	if len(w.outputLog) > 5000 {
		w.outputLog = w.outputLog[len(w.outputLog)-5000:]
	}
	w.mu.Unlock()
	select {
	case o.outputCh <- workerOutputMsg{name: w.name, text: text}:
	default:
	}
}

func (o *orchestrator) getWorkerOutput(name string) []string {
	w := o.getWorker(name)
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, len(w.outputLog))
	copy(out, w.outputLog)
	return out
}

func (o *orchestrator) addAuditWorker(buildWorkerName, repoPath string) {
	auditName := buildWorkerName + " (audit)"
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workers[auditName] = &workerState{
		name:       auditName,
		repoPath:   repoPath,
		status:     statusPending,
		isAudit:    true,
		parentName: buildWorkerName,
	}
}

func (o *orchestrator) runAuditWorker(w *workerState) {
	o.setStatus(w, statusRunning)

	prompt := securityAuditPreamble + "\n\nThe repository to audit is at: " + w.repoPath +
		"\nScan all source files. Fix every issue you find."

	os.MkdirAll(w.repoPath, 0o755)
	o.spawnClaude(w, prompt, false, 0)
}

func (o *orchestrator) startAuditWorker(auditName string) {
	w := o.getWorker(auditName)
	if w == nil {
		return
	}
	go o.runAuditWorker(w)
}

func (o *orchestrator) removeWorker(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.workers, name)
}

func (o *orchestrator) workerNames() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	names := make([]string, 0, len(o.workers))
	for n := range o.workers {
		names = append(names, n)
	}
	return names
}

func (o *orchestrator) runWorker(w *workerState) {
	o.setStatus(w, statusRunning)

	prompt := workerPreamble + "\n\n## Project Specification\n\n" + w.spec +
		"\n\nBuild this project now. The repository is at: " + w.repoPath +
		"\nStart by creating the project structure, then implement all features."

	os.MkdirAll(w.repoPath, 0o755)

	gitDir := filepath.Join(w.repoPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		cmd := exec.Command("git", "init")
		cmd.Dir = w.repoPath
		cmd.Run()
	}

	o.spawnClaude(w, prompt, false, 0)
}

func (o *orchestrator) resumeWorker(w *workerState, answer string) {
	o.setStatus(w, statusRunning)
	o.spawnClaude(w, answer, true, 0)
}

const maxQuestionDepth = 5

func (o *orchestrator) spawnClaude(w *workerState, prompt string, isContinue bool, questionDepth int) {
	// Use "--" to separate flags from prompt (prevents flag injection)
	args := []string{"-p", "--output-format", "stream-json", "--dangerously-skip-permissions"}

	w.mu.Lock()
	sid := w.sessionID
	w.mu.Unlock()

	if isContinue && sid != "" {
		args = append(args, "--resume", sid)
	}
	args = append(args, "--", prompt)

	cmd := exec.CommandContext(o.ctx, claudeBin, args...)
	cmd.Dir = w.repoPath

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		w.mu.Lock()
		w.errMsg = err.Error()
		w.mu.Unlock()
		o.setStatus(w, statusFailed)
		return
	}

	if err := cmd.Start(); err != nil {
		w.mu.Lock()
		w.errMsg = err.Error()
		w.mu.Unlock()
		o.setStatus(w, statusFailed)
		return
	}

	var assistantText strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		// Check for cancellation
		select {
		case <-o.ctx.Done():
			cmd.Process.Kill()
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			o.emitOutput(w, line)
			continue
		}

		eventType, _ := event["type"].(string)

		if eventType == "system" {
			if sid, ok := event["session_id"].(string); ok {
				w.mu.Lock()
				w.sessionID = sid
				w.mu.Unlock()
			}
		}

		if eventType == "assistant" {
			switch c := event["content"].(type) {
			case string:
				assistantText.WriteString(c)
				o.emitOutput(w, c)
			case []interface{}:
				for _, block := range c {
					if m, ok := block.(map[string]interface{}); ok {
						if m["type"] == "text" {
							if t, ok := m["text"].(string); ok {
								assistantText.WriteString(t)
								o.emitOutput(w, t)
							}
						}
					}
				}
			}
		}

		if eventType == "content_block_delta" {
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if delta["type"] == "text_delta" {
					if t, ok := delta["text"].(string); ok {
						assistantText.WriteString(t)
						o.emitOutput(w, t)
					}
				}
			}
		}

		if eventType == "result" {
			if sid, ok := event["session_id"].(string); ok {
				w.mu.Lock()
				w.sessionID = sid
				w.mu.Unlock()
			}
			switch r := event["result"].(type) {
			case string:
				assistantText.WriteString(r)
			case []interface{}:
				for _, block := range r {
					if m, ok := block.(map[string]interface{}); ok {
						if m["type"] == "text" {
							if t, ok := m["text"].(string); ok {
								assistantText.WriteString(t)
							}
						}
					}
				}
			}
		}
	}

	cmd.Wait()

	fullText := assistantText.String()
	if q := extractQuestion(fullText); q != nil {
		wq := &workerQuestion{workerName: w.name, text: q.Text, options: q.Options}
		w.mu.Lock()
		w.question = wq
		w.mu.Unlock()
		o.setStatus(w, statusWaiting)

		// Prevent unbounded manager->answer->spawn recursion
		if questionDepth < maxQuestionDepth {
			managerAnswer := o.askManager(w.name, q.Text)
			if managerAnswer != "" {
				o.emitOutput(w, fmt.Sprintf("\n[Manager answered: %s]\n", managerAnswer))
				w.mu.Lock()
				w.question = nil
				w.mu.Unlock()
				o.setStatus(w, statusRunning)
				o.spawnClaude(w, managerAnswer, true, questionDepth+1)
				return
			}
		}
		// Escalate to user
		select {
		case o.questionCh <- workerQuestionMsg{name: w.name, text: q.Text, options: q.Options}:
		default:
		}
	} else {
		o.setStatus(w, statusCompleted)
	}
}

type questionJSON struct {
	Type    string   `json:"type"`
	Text    string   `json:"text"`
	Options []string `json:"options"`
}

func extractQuestion(text string) *questionJSON {
	if !strings.Contains(text, questionMarker) {
		return nil
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var q questionJSON
		if err := json.Unmarshal([]byte(line), &q); err == nil {
			if q.Type == "question" && q.Text != "" {
				if len(q.Options) == 0 {
					q.Options = []string{"Yes", "No", "Other (let me explain)"}
				}
				return &q
			}
		}
	}
	return nil
}

func (o *orchestrator) askManager(workerName, questionText string) string {
	prompt := managerSystemPrompt + "\n\n## Master Project Specification\n\n" +
		o.masterSpec + "\n\n## Question from worker '" + workerName + "'\n\n" + questionText

	ctx, cancel := context.WithTimeout(o.ctx, time.Duration(managerTimeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, claudeBin, "-p", "--", prompt)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	response := strings.TrimSpace(string(out))
	if strings.Contains(strings.ToUpper(response), "ESCALATE") {
		return ""
	}
	return response
}

func prepareRepo(repoPath, name string) string {
	os.MkdirAll(repoPath, 0o755)
	gitDir := filepath.Join(repoPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		cmd := exec.Command("git", "init")
		cmd.Dir = repoPath
		cmd.Run()
	}
	return repoPath
}
