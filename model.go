package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// ── Message types ───────────────────────────────────────────

type tickMsg time.Time

type workerStatusMsg struct {
	name   string
	status workerStatus
}

type workerOutputMsg struct {
	name string
	text string
}

type workerQuestionMsg struct {
	name    string
	text    string
	options []string
}

type halResponseMsg struct {
	text       string
	specText   string
	startBuild bool
	err        error
}

// ── Chat message ────────────────────────────────────────────

type chatMessage struct {
	role    string // "hal", "user", "system", "worker", "question"
	name    string
	text    string
	options []string
}

// ── Model ───────────────────────────────────────────────────

type model struct {
	width, height int
	startTime     time.Time
	input         textarea.Model

	messages    []chatMessage
	halThinking bool // track thinking state instead of string matching

	workerStatuses  map[string]workerStatus
	conversation    *halConversation
	orch            *orchestrator
	voice           *halVoice
	projectSpecs    []projectSpec
	buildStarted    bool
	currentQuestion *workerQuestionMsg
	pendingQuestions []workerQuestionMsg
	lastHALSpoke    time.Time // when HAL last produced a message (for eye gaze)
	inspecting      string    // worker name being inspected ("" = normal chat)
}

func newModel() model {
	ti := textarea.New()
	ti.Placeholder = "Talk to HAL... (Ctrl+B build, Ctrl+Q quit)"
	ti.Focus()
	ti.CharLimit = 2000
	ti.ShowLineNumbers = false
	ti.SetHeight(3)
	ti.KeyMap.InsertNewline.SetEnabled(false) // Enter sends, not newline

	m := model{
		startTime:      time.Now(),
		input:          ti,
		workerStatuses: make(map[string]workerStatus),
		conversation:   &halConversation{},
		orch:           newOrchestrator(),
		voice:          newHALVoice(),
		projectSpecs:   listSpecs(),
	}

	// Restore session if available
	if sd := loadSession(); sd != nil {
		m.conversation.sessionID = sd.HALSessionID
		m.buildStarted = sd.BuildStarted

		// Restore worker statuses for display
		for _, ws := range sd.Workers {
			m.workerStatuses[ws.Name] = statusFromString(ws.Status)
			// Re-create worker state entries so they show in the panel
			m.orch.mu.Lock()
			m.orch.workers[ws.Name] = &workerState{
				name:      ws.Name,
				repoPath:  ws.RepoPath,
				status:    statusFromString(ws.Status),
				sessionID: ws.SessionID,
				isAudit:   ws.IsAudit,
			}
			m.orch.mu.Unlock()
		}

		greeting := timeGreeting()
		m.messages = append(m.messages, chatMessage{
			role: "hal",
			text: greeting + " I have restored our previous session. I am ready to continue.",
		})
		m.lastHALSpoke = time.Now()
		m.voice.sayShort(greeting + " Session restored.")

		if len(m.projectSpecs) > 0 {
			m.addMsg("system", "", fmt.Sprintf("Restored %d project(s) and %d worker(s).", len(m.projectSpecs), len(sd.Workers)), nil)
		}
	} else {
		greeting := timeGreeting()
		m.messages = append(m.messages, chatMessage{
			role: "hal",
			text: greeting + " I am HAL 9000. Tell me about the projects you would like to build.",
		})
		m.lastHALSpoke = time.Now()
		m.voice.sayShort(greeting + " I am HAL 9000.")

		if len(m.projectSpecs) > 0 {
			m.messages = append(m.messages, chatMessage{
				role: "system",
				text: fmt.Sprintf("Found %d existing project spec(s) in %s", len(m.projectSpecs), specsDir),
			})
		}
	}

	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		textarea.Blink,
		waitForStatus(m.orch.statusCh),
		waitForOutput(m.orch.outputCh),
		waitForQuestion(m.orch.questionCh),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second/eyeFPS, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func waitForStatus(ch <-chan workerStatusMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func waitForOutput(ch <-chan workerOutputMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func waitForQuestion(ch <-chan workerQuestionMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// addMsg appends a message and caps the history to maxMessages.
func (m *model) addMsg(role, name, text string, options []string) {
	m.messages = append(m.messages, chatMessage{role: role, name: name, text: text, options: options})
	if len(m.messages) > maxMessages {
		// Keep last maxMessages entries, drop oldest
		m.messages = m.messages[len(m.messages)-maxMessages:]
	}
}

// ── Update ──────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(max(20, msg.Width-28))

	case tickMsg:
		cmds = append(cmds, tickCmd())

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			m.orch.shutdown()
			return m, tea.Quit
		case "ctrl+b":
			var cmd tea.Cmd
			m, cmd = handleStartBuild(m)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "ctrl+y":
			if text := lastHALMessage(m.messages); text != "" {
				copyToClipboard(text)
				m.addMsg("system", "", "Copied last HAL response to clipboard.", nil)
			}
		case "ctrl+w":
			m = cycleInspect(m)
		case "esc":
			if m.inspecting != "" {
				m.inspecting = ""
				m.addMsg("system", "", "Exited inspection mode.", nil)
			}
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text != "" {
				m.input.SetValue("")
				m.addMsg("user", "", text, nil)

				// Handle slash commands
				if cmd := parseCommand(text); cmd != nil {
					switch cmd.name {
					case "discontinue":
						m = handleDiscontinue(m, cmd.arg)
						return m, tea.Batch(cmds...)
					case "inspect":
						if cmd.arg != "" {
							m.inspecting = cmd.arg
							m.addMsg("system", "", fmt.Sprintf("Inspecting worker: %s (Esc to exit)", cmd.arg), nil)
						}
						return m, tea.Batch(cmds...)
					case "scan":
						if cmd.arg != "" {
							scanPath := resolvePath(cmd.arg)
							m.halThinking = true
							m.addMsg("system", "", fmt.Sprintf("Scanning repository: %s ...", scanPath), nil)
							conv := m.conversation
							cmds = append(cmds, scanAndSendToHAL(conv, scanPath, ""))
						} else {
							m.addMsg("system", "", "Usage: /scan <path-to-repo>", nil)
						}
						return m, tea.Batch(cmds...)
					}
				}

				if m.currentQuestion != nil {
					var cmd tea.Cmd
					m, cmd = handleQuestionAnswer(m, text)
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
				} else {
					// Auto-detect repo paths in natural language
					if repoPath := detectRepoPath(text); repoPath != "" {
						m.halThinking = true
						m.addMsg("system", "", fmt.Sprintf("Detected repo path: %s — scanning...", repoPath), nil)
						cmds = append(cmds, scanAndSendToHAL(m.conversation, repoPath, text))
					} else {
						m.halThinking = true
						m.addMsg("system", "", "HAL is thinking...", nil)
						cmds = append(cmds, sendToHAL(m.conversation, text))
					}
				}
			}
			return m, tea.Batch(cmds...)
		}

	case halResponseMsg:
		// Remove thinking indicator
		if m.halThinking {
			m.halThinking = false
			// Remove the last "thinking" message by scanning backwards
			for i := len(m.messages) - 1; i >= 0; i-- {
				if m.messages[i].role == "system" && m.messages[i].text == "HAL is thinking..." {
					m.messages = append(m.messages[:i], m.messages[i+1:]...)
					break
				}
			}
		}
		if msg.err != nil {
			m.addMsg("system", "", "Error: "+msg.err.Error(), nil)
		} else {
			m.addMsg("hal", "", msg.text, nil)
			m.lastHALSpoke = time.Now()
			m.voice.sayShort(firstSentence(msg.text))

			// Process memory tags
			if projName, content := extractMemory(msg.text); projName != "" {
				if err := saveMemory(projName, content); err == nil {
					m.addMsg("system", "", fmt.Sprintf("Saved knowledge about '%s' to memory.", projName), nil)
				}
			}

			// Process discontinue tags
			if projName := extractDiscontinue(msg.text); projName != "" {
				m = handleDiscontinue(m, projName)
			}

			if msg.specText != "" {
				m = handleNewSpec(m, msg.specText)
			}
			if msg.startBuild {
				var cmd tea.Cmd
				m, cmd = handleStartBuild(m)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			saveSession(&m)
		}

	case workerStatusMsg:
		m.workerStatuses[msg.name] = msg.status
		switch msg.status {
		case statusCompleted:
			w := m.orch.getWorker(msg.name)
			if w != nil && w.isAudit {
				// Audit worker completed — mark parent as done
				m.addMsg("system", "", fmt.Sprintf("Security audit for '%s' passed.", w.parentName), nil)
				m.workerStatuses[w.parentName] = statusDone
				m.voice.sayShort(w.parentName + " audit complete.")
			} else if w != nil && !w.isAudit {
				// Build worker completed — spawn security audit
				m.addMsg("system", "", fmt.Sprintf("Worker '%s' completed. Starting security audit...", msg.name), nil)
				m.workerStatuses[msg.name] = statusAuditing
				m.voice.sayShort(msg.name + " complete. Auditing.")
				auditName := msg.name + " (audit)"
				m.orch.addAuditWorker(msg.name, w.repoPath)
				m.workerStatuses[auditName] = statusPending
				orch := m.orch
				cmds = append(cmds, func() tea.Msg {
					orch.startAuditWorker(auditName)
					return nil
				})
			}
		case statusFailed:
			w := m.orch.getWorker(msg.name)
			if w != nil && w.isAudit {
				// Audit worker failed
				m.addMsg("system", "", fmt.Sprintf("Security audit FAILED for '%s'.", w.parentName), nil)
				m.workerStatuses[w.parentName] = statusAuditFailed
			} else {
				errStr := "unknown"
				if w != nil {
					w.mu.Lock()
					errStr = w.errMsg
					w.mu.Unlock()
				}
				m.addMsg("system", "", fmt.Sprintf("Worker '%s' FAILED: %s", msg.name, errStr), nil)
			}
		case statusWaiting:
			m.addMsg("system", "", fmt.Sprintf("Worker '%s' has a question...", msg.name), nil)
		}
		saveSession(&m)
		cmds = append(cmds, waitForStatus(m.orch.statusCh))

	case workerOutputMsg:
		text := strings.TrimSpace(msg.text)
		if text != "" {
			if len(text) > 500 {
				text = text[:500] + "..."
			}
			m.addMsg("worker", msg.name, text, nil)
		}
		cmds = append(cmds, waitForOutput(m.orch.outputCh))

	case workerQuestionMsg:
		m.pendingQuestions = append(m.pendingQuestions, msg)
		if m.currentQuestion == nil && len(m.pendingQuestions) > 0 {
			q := m.pendingQuestions[0]
			m.pendingQuestions = m.pendingQuestions[1:]
			m.currentQuestion = &q
			m.addMsg("question", q.name, q.text, q.options)
			m.voice.sayShort("Question from " + q.name)
		}
		cmds = append(cmds, waitForQuestion(m.orch.questionCh))
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	return m, tea.Batch(cmds...)
}

// ── Command helpers ─────────────────────────────────────────

func sendToHAL(conv *halConversation, text string) tea.Cmd {
	return func() tea.Msg {
		response, err := conv.send(text)
		if err != nil {
			return halResponseMsg{err: err}
		}
		return halResponseMsg{
			text:       response,
			specText:   conv.extractSpec(response),
			startBuild: conv.checkStartBuild(response),
		}
	}
}

func scanAndSendToHAL(conv *halConversation, repoPath, userText string) tea.Cmd {
	return func() tea.Msg {
		analysis, err := scanRepo(repoPath)
		if err != nil {
			return halResponseMsg{err: fmt.Errorf("scan failed: %w", err)}
		}

		var prompt strings.Builder
		prompt.WriteString("I'm pointing you at an existing repository. Here's what's in it:\n\n")
		prompt.WriteString(analysis)
		prompt.WriteString("\n\n")
		if userText != "" {
			prompt.WriteString("User's message: ")
			prompt.WriteString(userText)
		} else {
			prompt.WriteString("Analyze this project. Tell me what you see, then ask me questions about what I want to do with it — what to change, add, fix, or build next.")
		}

		response, err := conv.send(prompt.String())
		if err != nil {
			return halResponseMsg{err: err}
		}
		return halResponseMsg{
			text:       response,
			specText:   conv.extractSpec(response),
			startBuild: conv.checkStartBuild(response),
		}
	}
}

func handleQuestionAnswer(m model, text string) (model, tea.Cmd) {
	q := m.currentQuestion
	if q == nil {
		return m, nil
	}

	answer := text
	if idx, err := strconv.Atoi(text); err == nil && idx >= 1 && idx <= len(q.options) {
		answer = q.options[idx-1]
	}

	m.addMsg("system", "", fmt.Sprintf("Answering %s: %s", q.name, answer), nil)
	m.voice.acknowledge()

	orch := m.orch
	name := q.name
	m.currentQuestion = nil

	if len(m.pendingQuestions) > 0 {
		next := m.pendingQuestions[0]
		m.pendingQuestions = m.pendingQuestions[1:]
		m.currentQuestion = &next
		m.addMsg("question", next.name, next.text, next.options)
	}

	return m, func() tea.Msg {
		orch.answerQuestion(name, answer)
		return nil
	}
}

func handleNewSpec(m model, specText string) model {
	name := "unnamed_project"
	for _, line := range strings.Split(specText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			name = strings.TrimSpace(strings.TrimLeft(line, "#"))
			break
		} else if line != "" && !strings.HasPrefix(line, "-") {
			if len(line) > 60 {
				name = line[:60]
			} else {
				name = line
			}
			break
		}
	}

	desc := ""
	for i, line := range strings.Split(specText, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && i > 0 {
			if len(line) > 200 {
				desc = line[:200]
			} else {
				desc = line
			}
			break
		}
	}

	// Strict ASCII-only sanitization for path safety
	re := regexp.MustCompile(`[^a-z0-9\-]`)
	safeName := re.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	if safeName == "" || safeName == "-" {
		safeName = "project"
	}
	safeName = filepath.Base(safeName) // strip any path separators
	repoPath := filepath.Join(filepath.Dir(specsDir), safeName)

	filePath, err := saveSpec(name, desc, repoPath, specText)
	if err != nil {
		m.addMsg("system", "", fmt.Sprintf("Failed to save spec: %v", err), nil)
		return m
	}

	m.projectSpecs = append(m.projectSpecs, projectSpec{
		name: name, description: desc, repoPath: repoPath,
		specText: specText, filePath: filePath,
	})
	m.addMsg("system", "", fmt.Sprintf("Saved spec: %s -> %s", name, filePath), nil)
	m.addMsg("system", "", fmt.Sprintf("Total projects: %d. Ctrl+B to build.", len(m.projectSpecs)), nil)
	saveSession(&m)
	return m
}

func handleStartBuild(m model) (model, tea.Cmd) {
	if m.buildStarted {
		m.addMsg("system", "", "Build already in progress.", nil)
		return m, nil
	}
	if len(m.projectSpecs) == 0 {
		m.addMsg("system", "", "No project specs defined yet.", nil)
		return m, nil
	}

	m.buildStarted = true
	m.orch.masterSpec = buildMasterSpec(m.projectSpecs)

	m.addMsg("hal", "", fmt.Sprintf("Initiating build sequence for %d project(s). All workers deployed.", len(m.projectSpecs)), nil)
	m.lastHALSpoke = time.Now()
	m.voice.sayShort("Initiating build sequence.")

	for i := range m.projectSpecs {
		spec := &m.projectSpecs[i]
		spec.repoPath = prepareRepo(spec.repoPath, spec.name)
		m.orch.addWorker(spec.name, spec.repoPath, spec.specText)
		m.workerStatuses[spec.name] = statusPending
	}

	saveSession(&m)

	orch := m.orch
	return m, func() tea.Msg {
		orch.startAll()
		return nil
	}
}

func firstSentence(text string) string {
	// Find the first sentence ending that's followed by a space or end-of-string,
	// and is at least 10 chars in to skip abbreviations like "Dr." or "v1.0"
	for i, c := range text {
		if (c == '.' || c == '!' || c == '?') && i >= 10 {
			next := i + 1
			if next >= len(text) || text[next] == ' ' || text[next] == '\n' {
				return text[:next]
			}
		}
	}
	if len(text) > 80 {
		return text[:80]
	}
	return text
}

func timeGreeting() string {
	h := time.Now().Hour()
	switch {
	case h < 4:
		return "Working late, I see."
	case h < 12:
		return "Good morning."
	case h < 17:
		return "Good afternoon."
	case h < 21:
		return "Good evening."
	default:
		return "Burning the midnight oil, I see."
	}
}

func handleDiscontinue(m model, projectName string) model {
	found := -1
	for i, p := range m.projectSpecs {
		if strings.EqualFold(p.name, projectName) {
			found = i
			break
		}
	}
	if found < 0 {
		m.addMsg("system", "", fmt.Sprintf("Project '%s' not found.", projectName), nil)
		return m
	}

	spec := m.projectSpecs[found]
	m.projectSpecs = append(m.projectSpecs[:found], m.projectSpecs[found+1:]...)

	// Remove worker and audit worker
	delete(m.workerStatuses, spec.name)
	delete(m.workerStatuses, spec.name+" (audit)")
	m.orch.removeWorker(spec.name)
	m.orch.removeWorker(spec.name + " (audit)")

	// Remove spec file (but not the repo directory)
	if spec.filePath != "" {
		os.Remove(spec.filePath)
	}
	// Remove memory file
	deleteMemory(spec.name)

	m.addMsg("system", "", fmt.Sprintf("Discontinued project '%s'.", spec.name), nil)
	m.voice.sayShort("Project discontinued.")
	saveSession(&m)
	return m
}

func cycleInspect(m model) model {
	names := m.orch.workerNames()
	if len(names) == 0 {
		m.addMsg("system", "", "No workers to inspect.", nil)
		return m
	}
	sort.Strings(names)

	if m.inspecting == "" {
		m.inspecting = names[0]
		m.addMsg("system", "", fmt.Sprintf("Inspecting: %s (Ctrl+W cycle, Esc exit)", m.inspecting), nil)
		return m
	}

	// Find current index and cycle
	for i, n := range names {
		if n == m.inspecting {
			if i+1 < len(names) {
				m.inspecting = names[i+1]
				m.addMsg("system", "", fmt.Sprintf("Inspecting: %s", m.inspecting), nil)
			} else {
				m.inspecting = ""
				m.addMsg("system", "", "Exited inspection mode.", nil)
			}
			return m
		}
	}

	// Current not found, start over
	m.inspecting = names[0]
	return m
}

func lastHALMessage(messages []chatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].role == "hal" {
			return messages[i].text
		}
	}
	return ""
}

func copyToClipboard(text string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	default:
		return
	}
	cmd.Stdin = strings.NewReader(text)
	cmd.Run()
}
