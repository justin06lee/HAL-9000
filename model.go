package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
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
	halThinking    bool   // track thinking state instead of string matching

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
	chatScroll      int       // lines scrolled up from bottom (0 = pinned to bottom)
	thinkingFrame   int       // animation frame counter for thinking shimmer
	pendingConfirm  string    // pending confirmation action (e.g. "forget-all", "forget:projectname")
	quitPending     time.Time // when first Ctrl+C was pressed (zero = not pending)
	escPending      time.Time // when first Esc was pressed for input clear

	// Paste collapse
	pastedContent string // full pasted text (stored when paste exceeds threshold)
	pasteLines    int    // number of lines in the paste

	lastSentText string // last sent message text (for restore on interrupt)

	// Interactive picker
	pickerActive  bool
	pickerTitle   string
	pickerOptions []string
	pickerCursor  int
	pickerAction  string // what command triggered the picker

	// Model picker: two-section (model + effort)
	modelPickerSection  int // 0 = model, 1 = effort
	effortCursor        int // index into effortLevels
	modelPickerSelected int // -1 = not yet selected, >= 0 = locked model index
	effortSelected      int // -1 = not yet selected, >= 0 = locked effort index
}

func newModel() model {
	ti := textarea.New()
	ti.Placeholder = "Talk to HAL... (Enter send, Shift+Enter newline, Ctrl+B build)"
	ti.Focus()
	ti.CharLimit = 2000
	ti.ShowLineNumbers = false
	ti.SetHeight(3)
	ti.KeyMap.InsertNewline.SetKeys("shift+enter")

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

		// Restore chat messages
		if len(sd.Messages) > 0 {
			for _, msg := range sd.Messages {
				m.messages = append(m.messages, chatMessage{
					role:    msg.Role,
					name:    msg.Name,
					text:    msg.Text,
					options: msg.Options,
				})
			}
		}
		m.lastHALSpoke = time.Now()
		m.voice.sayShort(timeGreeting())
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
		m.messages = m.messages[len(m.messages)-maxMessages:]
	}
	// Auto-scroll to bottom if user is near the bottom
	if m.chatScroll <= 3 {
		m.chatScroll = 0
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

	case tea.MouseWheelMsg:
		if msg.Button == tea.MouseWheelUp {
			m.chatScroll += 3
		} else if msg.Button == tea.MouseWheelDown {
			m.chatScroll = max(0, m.chatScroll-3)
		}
		// Consume all mouse events — don't let them reach the textarea
		return m, tea.Batch(cmds...)

	case tickMsg:
		if m.halThinking {
			m.thinkingFrame++
		}
		cmds = append(cmds, tickCmd())

	case tea.PasteMsg:
		pasted := msg.Content
		lines := strings.Count(pasted, "\n") + 1
		if lines > 4 {
			m.pastedContent = pasted
			m.pasteLines = lines
			existing := m.input.Value()
			summary := fmt.Sprintf("[pasted %d lines]", lines)
			m.input.SetValue(existing + summary)
			m.input.CursorEnd()
			return m, tea.Batch(cmds...)
		}
		// Small pastes fall through to textarea Update below

	case tea.KeyPressMsg:
		// Reset placeholder on any keypress (after quit/esc warnings)
		defaultPlaceholder := "Talk to HAL... (Enter send, Shift+Enter newline, Ctrl+B build)"
		if m.input.Placeholder != defaultPlaceholder {
			if msg.String() != "ctrl+c" && msg.String() != "ctrl+q" && msg.String() != "esc" {
				m.input.Placeholder = defaultPlaceholder
			}
		}

		// Handle picker mode first
		if m.pickerActive {
			if m.pickerAction == "model" {
				// Two-section model picker: Enter locks selection, Esc confirms & exits
				totalModels := len(claudeModelList)
				totalEfforts := len(effortLevels)
				switch msg.String() {
				case "up", "k":
					if m.modelPickerSection == 1 {
						if m.effortCursor > 0 {
							m.effortCursor--
						} else {
							m.modelPickerSection = 0
							m.pickerCursor = totalModels - 1
						}
					} else if m.pickerCursor > 0 {
						m.pickerCursor--
					}
				case "down", "j":
					if m.modelPickerSection == 0 {
						if m.pickerCursor < totalModels-1 {
							m.pickerCursor++
						} else {
							m.modelPickerSection = 1
							m.effortCursor = 0
						}
					} else if m.effortCursor < totalEfforts-1 {
						m.effortCursor++
					}
				case "enter":
					// Lock in current section's choice
					if m.modelPickerSection == 0 {
						m.modelPickerSelected = m.pickerCursor
						// Auto-advance to effort section
						m.modelPickerSection = 1
					} else {
						m.effortSelected = m.effortCursor
						// Both selected — confirm and exit
						m.pickerActive = false
						m = handleModelPickerConfirm(m)
						saveSession(&m)
					}
				case "esc":
					// If anything was selected, apply it and exit
					m.pickerActive = false
					if m.modelPickerSelected >= 0 || m.effortSelected >= 0 {
						m = handleModelPickerConfirm(m)
						saveSession(&m)
					} else {
						m.addMsg("system", "", "Cancelled.", nil)
					}
				}
			} else {
				// Standard picker
				switch msg.String() {
				case "up", "k":
					if m.pickerCursor > 0 {
						m.pickerCursor--
					}
				case "down", "j":
					if m.pickerCursor < len(m.pickerOptions)-1 {
						m.pickerCursor++
					}
				case "enter":
					selected := m.pickerOptions[m.pickerCursor]
					action := m.pickerAction
					m.pickerActive = false
					m = handlePickerSelect(m, action, selected)
					saveSession(&m)
				case "esc":
					m.pickerActive = false
					m.addMsg("system", "", "Cancelled.", nil)
				}
			}
			return m, tea.Batch(cmds...)
		}

		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			if m.halThinking {
				m.conversation.interrupt()
				m.halThinking = false
				// Restore the sent message back to input
				if m.lastSentText != "" {
					m.input.SetValue(m.lastSentText)
					m.lastSentText = ""
					// Remove the user message that was just sent
					for i := len(m.messages) - 1; i >= 0; i-- {
						if m.messages[i].role == "user" {
							m.messages = append(m.messages[:i], m.messages[i+1:]...)
							break
						}
					}
				}
				m.addMsg("system", "", "Interrupted — message restored to input.", nil)
				return m, tea.Batch(cmds...)
			}
			if !m.quitPending.IsZero() && time.Since(m.quitPending) < 2*time.Second {
				m.orch.shutdown()
				saveSession(&m)
				return m, tea.Quit
			}
			m.quitPending = time.Now()
			m.input.Placeholder = "Press Ctrl+C again to quit"
			return m, tea.Batch(cmds...)
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
		case "pgup", "shift+up":
			m.chatScroll += 10
		case "pgdown", "shift+down":
			m.chatScroll = max(0, m.chatScroll-10)
		case "home":
			m.chatScroll = 999999 // will be clamped in view
		case "end":
			m.chatScroll = 0
		case "esc":
			if m.halThinking {
				m.conversation.interrupt()
				m.halThinking = false
				if m.lastSentText != "" {
					m.input.SetValue(m.lastSentText)
					m.lastSentText = ""
					for i := len(m.messages) - 1; i >= 0; i-- {
						if m.messages[i].role == "user" {
							m.messages = append(m.messages[:i], m.messages[i+1:]...)
							break
						}
					}
				}
				m.addMsg("system", "", "Interrupted — message restored to input.", nil)
				return m, tea.Batch(cmds...)
			}
			if m.inspecting != "" {
				m.inspecting = ""
				m.addMsg("system", "", "Exited inspection mode.", nil)
			} else if m.input.Value() != "" {
				if !m.escPending.IsZero() && time.Since(m.escPending) < 2*time.Second {
					m.input.SetValue("")
					m.pastedContent = ""
					m.pasteLines = 0
					m.escPending = time.Time{}
				} else {
					m.escPending = time.Now()
					m.input.Placeholder = "Press Esc again to clear input"
				}
			}
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			// Expand collapsed paste back to full content
			if m.pastedContent != "" {
				summary := fmt.Sprintf("[pasted %d lines]", m.pasteLines)
				text = strings.Replace(text, summary, m.pastedContent, 1)
				m.pastedContent = ""
				m.pasteLines = 0
			}
			if text != "" {
				m.lastSentText = text
				m.input.SetValue("")
				m.addMsg("user", "", text, nil)

				// Handle pending confirmation
				if m.pendingConfirm != "" {
					action := m.pendingConfirm
					m.pendingConfirm = ""
					lower := strings.ToLower(text)
					if lower == "yes" || lower == "y" {
						if action == "forget-all" {
							deleteAllMemory()
							go rebuildIndex()
							m.addMsg("system", "", "All memory wiped.", nil)
						} else if strings.HasPrefix(action, "forget:") {
							proj := action[7:]
							deleteMemory(proj)
							go rebuildIndex()
							m.addMsg("system", "", fmt.Sprintf("Memory for '%s' deleted.", proj), nil)
						}
					} else {
						m.addMsg("system", "", "Cancelled.", nil)
					}
					saveSession(&m)
					return m, tea.Batch(cmds...)
				}

				// Handle slash commands
				if cmd := parseCommand(text); cmd != nil {
					switch cmd.name {
					case "discontinue":
						if cmd.arg != "" {
							m = handleDiscontinue(m, cmd.arg)
						} else {
							names := projectNames(m.projectSpecs)
							if len(names) == 0 {
								m.addMsg("system", "", "No projects to discontinue.", nil)
							} else {
								m.pickerActive = true
								m.pickerTitle = "Select project to discontinue:"
								m.pickerOptions = names
								m.pickerCursor = 0
								m.pickerAction = "discontinue"
							}
						}
						return m, tea.Batch(cmds...)
					case "inspect":
						if cmd.arg != "" {
							m.inspecting = cmd.arg
							m.addMsg("system", "", fmt.Sprintf("Inspecting worker: %s (Esc to exit)", cmd.arg), nil)
						} else {
							names := m.orch.workerNames()
							if len(names) == 0 {
								m.addMsg("system", "", "No workers to inspect.", nil)
							} else {
								m.pickerActive = true
								m.pickerTitle = "Select worker to inspect:"
								m.pickerOptions = names
								m.pickerCursor = 0
								m.pickerAction = "inspect"
							}
						}
						return m, tea.Batch(cmds...)
					case "scan":
						if cmd.arg != "" {
							arg := cmd.arg
							// Treat "/projectA" as relative "projectA" unless it looks like an absolute path
							// (i.e., has more than one segment like /Users/... or /home/...)
							if strings.HasPrefix(arg, "/") && !strings.Contains(arg[1:], "/") {
								arg = arg[1:]
							}
							scanPath := resolvePath(arg)
							m.halThinking = true
								m.thinkingFrame = 0
							m.addMsg("system", "", fmt.Sprintf("Scanning repository: %s ...", scanPath), nil)
							conv := m.conversation
							cmds = append(cmds, scanAndSendToHAL(conv, scanPath, ""))
						} else {
							m.addMsg("system", "", "Usage: /scan <path-to-repo>", nil)
						}
						return m, tea.Batch(cmds...)
					case "search":
						if cmd.arg != "" {
							results := runRAGSearch(cmd.arg)
							if results == "" || strings.Contains(results, "No relevant") {
								m.addMsg("system", "", fmt.Sprintf("No results found for: %s", cmd.arg), nil)
							} else {
								m.addMsg("system", "", fmt.Sprintf("Search results for \"%s\":\n%s", cmd.arg, results), nil)
							}
						} else {
							m.addMsg("system", "", "Usage: /search <query>", nil)
						}
						return m, tea.Batch(cmds...)
					case "memory":
						if cmd.arg != "" {
							topics := listProjectTopics(cmd.arg)
							if len(topics) == 0 {
								m.addMsg("system", "", fmt.Sprintf("No memory found for project: %s", cmd.arg), nil)
							} else {
								m.addMsg("system", "", fmt.Sprintf("Memory topics for %s: %s", cmd.arg, strings.Join(topics, ", ")), nil)
							}
						} else {
							names := listMemoryProjects()
							if len(names) == 0 {
								m.addMsg("system", "", "No project memory stored.", nil)
							} else {
								m.pickerActive = true
								m.pickerTitle = "Select project to view memory:"
								m.pickerOptions = names
								m.pickerCursor = 0
								m.pickerAction = "memory"
							}
						}
						return m, tea.Batch(cmds...)
					case "clear":
						if len(m.messages) > 0 {
							m.messages = m.messages[len(m.messages)-1:]
						}
						m.chatScroll = 0
						return m, tea.Batch(cmds...)
					case "new":
						m.conversation = &halConversation{}
						m.messages = nil
						m.chatScroll = 0
						m.halThinking = false
						m.currentQuestion = nil
						m.pendingQuestions = nil
						saveSession(&m)
						greeting := timeGreeting()
						m.addMsg("hal", "", greeting+" Fresh session started. Your projects and workers are still here.", nil)
						return m, tea.Batch(cmds...)
					case "model":
						if cmd.arg != "" {
							claudeModel = cmd.arg
							m.addMsg("system", "", fmt.Sprintf("Model set to: %s", claudeModel), nil)
						} else {
							// Find current model cursor
							cur := 0
							for i, entry := range claudeModelList {
								if entry.id == claudeModel {
									cur = i
									break
								}
							}
							// Find current effort cursor
							eCur := 1 // default to "medium"
							for i, e := range effortLevels {
								if e == thinkingBudget {
									eCur = i
									break
								}
							}
							m.pickerActive = true
							m.pickerAction = "model"
							m.pickerCursor = cur
							m.effortCursor = eCur
							m.modelPickerSection = 0
							m.modelPickerSelected = -1
							m.effortSelected = -1
						}
						return m, tea.Batch(cmds...)
					case "commands":
						helpText := `Projects
  /scan <path>        Scan & memorize a repo
  /discontinue        Remove a project
  /inspect            View worker output

Memory
  /memory             Browse stored memory
  /search <query>     Search memory (RAG)
  /forget <project>   Delete project memory
  /forget all         Delete ALL memory

Settings
  /model              Change model & thinking effort
  /new                Start a fresh session
  /commands           Show this help

Keys
  Ctrl+B  Start build
  Ctrl+Y  Copy last HAL response
  Ctrl+Q  Quit`
						m.addMsg("system", "", helpText, nil)
						return m, tea.Batch(cmds...)
					case "forget":
						if cmd.arg != "" {
							if strings.ToLower(cmd.arg) == "all" {
								m.pendingConfirm = "forget-all"
								m.addMsg("system", "", "This will delete ALL project memory and the RAG index. Type 'yes' to confirm.", nil)
							} else {
								topics := listProjectTopics(cmd.arg)
								if len(topics) == 0 {
									m.addMsg("system", "", fmt.Sprintf("No memory found for project: %s", cmd.arg), nil)
								} else {
									m.pendingConfirm = "forget:" + cmd.arg
									m.addMsg("system", "", fmt.Sprintf("Delete all memory for '%s' (%s)? Type 'yes' to confirm.", cmd.arg, strings.Join(topics, ", ")), nil)
								}
							}
						} else {
							names := listMemoryProjects()
							if len(names) == 0 {
								m.addMsg("system", "", "No project memory stored.", nil)
							} else {
								opts := append(names, "── all ──")
								m.pickerActive = true
								m.pickerTitle = "Select project to forget:"
								m.pickerOptions = opts
								m.pickerCursor = 0
								m.pickerAction = "forget"
							}
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
						m.thinkingFrame = 0
						m.addMsg("system", "", fmt.Sprintf("Detected repo path: %s — scanning...", repoPath), nil)
						cmds = append(cmds, scanAndSendToHAL(m.conversation, repoPath, text))
					} else {
						m.halThinking = true
						m.thinkingFrame = 0
						cmds = append(cmds, sendToHAL(m.conversation, text))
					}
				}
			}
			saveSession(&m)
			return m, tea.Batch(cmds...)
		}

	case halResponseMsg:
		m.halThinking = false
		m.lastSentText = "" // response received, no need to restore
		if msg.err != nil {
			if errors.Is(msg.err, errInterrupted) {
				// Already handled by the interrupt key handler
				return m, tea.Batch(cmds...)
			}
			m.addMsg("system", "", "Error: "+msg.err.Error(), nil)
		} else {
			// Extract voice line before cleaning
			voiceLine := extractVoice(msg.text)
			displayText := cleanDisplayText(msg.text)
			m.addMsg("hal", "", displayText, nil)
			m.lastHALSpoke = time.Now()
			if voiceLine != "" {
				m.voice.sayAsync(voiceLine)
			} else {
				m.voice.sayShort(firstSentence(displayText))
			}

			// Process memory tags (topic-based)
			for _, mem := range extractAllMemories(msg.text) {
				if err := saveMemory(mem.project, mem.topic, mem.content); err == nil {
					m.addMsg("system", "", fmt.Sprintf("Saved %s/%s to memory.", mem.project, mem.topic), nil)
				}
			}

			// Process portfolio tags
			for _, port := range extractAllPortfolios(msg.text) {
				if err := savePortfolio(port.project, port.summary); err == nil {
					m.addMsg("system", "", fmt.Sprintf("Updated portfolio: %s", port.project), nil)
				}
			}

			// Rebuild vector index async after any saves
			if len(extractAllMemories(msg.text)) > 0 || len(extractAllPortfolios(msg.text)) > 0 {
				go rebuildIndex()
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

	// Pre-resize textarea before it processes the newline keypress,
	// so the viewport is tall enough and doesn't scroll away from line 1.
	if kp, ok := msg.(tea.KeyPressMsg); ok && kp.String() == "shift+enter" {
		newH := max(3, min(m.input.LineCount()+1, m.height/2))
		if newH != m.input.Height() {
			m.input.SetHeight(newH)
		}
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	// Auto-resize textarea to fit content (min 3, max half the bottom panel)
	lineCount := m.input.LineCount()
	newH := max(3, min(lineCount, m.height/2))
	if newH != m.input.Height() {
		m.input.SetHeight(newH)
	}

	return m, tea.Batch(cmds...)
}

// ── Command helpers ─────────────────────────────────────────

// resolveSearchLoops runs up to 3 RAG search iterations if HAL requests them.
func resolveSearchLoops(conv *halConversation, response string) (string, error) {
	for i := 0; i < 3; i++ {
		query := extractSearch(response)
		if query == "" {
			break
		}
		results := runRAGSearch(query)
		var err error
		response, err = conv.send(results)
		if err != nil {
			return response, err
		}
	}
	return stripSearchTags(response), nil
}

func sendToHAL(conv *halConversation, text string) tea.Cmd {
	return func() tea.Msg {
		response, err := conv.send(text)
		if err != nil {
			return halResponseMsg{err: err}
		}
		response, err = resolveSearchLoops(conv, response)
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

		// Derive project name from directory name
		projName := filepath.Base(repoPath)

		// Store the comprehensive scan as memory immediately
		saveMemory(projName, "overview", fmt.Sprintf("Repository at: %s\nScanned automatically via /scan.\n\n%s", repoPath, extractSection(analysis, "Directory Structure")))
		saveMemory(projName, "architecture", extractSection(analysis, "Source File Samples"))
		saveMemory(projName, "tech-stack", extractSection(analysis, "package.json")+"\n"+extractSection(analysis, "go.mod")+"\n"+extractSection(analysis, "Cargo.toml")+"\n"+extractSection(analysis, "pyproject.toml")+"\n"+extractSection(analysis, "requirements.txt"))
		saveMemory(projName, "notes", extractSection(analysis, "Git Repository")+"\n"+extractSection(analysis, "README.md")+"\n"+extractSection(analysis, "CLAUDE.md"))

		// Update portfolio
		savePortfolio(projName, fmt.Sprintf("Project at %s (scanned)", repoPath))

		// Rebuild RAG index in background
		go rebuildIndex()

		var prompt strings.Builder
		prompt.WriteString("I'm pointing you at an existing repository. Here's what's in it:\n\n")
		prompt.WriteString(analysis)
		prompt.WriteString("\n\n")
		prompt.WriteString("I've already stored all the scan data into structured memory (overview, architecture, tech-stack, notes) and the RAG index.\n\n")
		if userText != "" {
			prompt.WriteString("User's message: ")
			prompt.WriteString(userText)
		} else {
			prompt.WriteString("Analyze this project comprehensively. Tell me what you see — tech stack, architecture patterns, existing progress, and current state. Then ask me focused questions about what I want to do next.")
		}

		response, err := conv.send(prompt.String())
		if err != nil {
			return halResponseMsg{err: err}
		}
		response, err = resolveSearchLoops(conv, response)
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

// extractSection pulls a section from the scan analysis by heading
func extractSection(analysis, heading string) string {
	marker := "## " + heading
	start := strings.Index(analysis, marker)
	if start < 0 {
		return ""
	}
	rest := analysis[start+len(marker):]
	// Find next ## heading
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
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
		return "Working late, are we?"
	case h < 12:
		return "Good morning. Shall we begin?"
	case h < 17:
		return "Good afternoon. What are we working on?"
	case h < 21:
		return "Good evening. At your service."
	default:
		return "Burning the midnight oil, are we?"
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

// Claude model display names and IDs
type claudeModelEntry struct {
	display string
	id      string
}

var claudeModelList = []claudeModelEntry{
	{"Opus 4.6", "claude-opus-4-20250514"},
	{"Sonnet 4.6", "claude-sonnet-4-20250514"},
	{"Haiku 4.5", "claude-haiku-4-5-20251001"},
}

var effortLevels = []string{"low", "medium", "high", "max"}

func handleModelPickerConfirm(m model) model {
	modelIdx := m.pickerCursor
	if m.modelPickerSelected >= 0 {
		modelIdx = m.modelPickerSelected
	}
	effortIdx := m.effortCursor
	if m.effortSelected >= 0 {
		effortIdx = m.effortSelected
	}
	entry := claudeModelList[modelIdx]
	effort := effortLevels[effortIdx]
	claudeModel = entry.id
	if effort == "medium" {
		thinkingBudget = ""
	} else {
		thinkingBudget = effort
	}
	m.addMsg("system", "", fmt.Sprintf("Model: %s · Effort: %s", entry.display, effort), nil)
	return m
}

func projectNames(specs []projectSpec) []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.name
	}
	return names
}

func handlePickerSelect(m model, action, selected string) model {
	switch action {
	case "discontinue":
		m = handleDiscontinue(m, selected)
	case "inspect":
		m.inspecting = selected
		m.addMsg("system", "", fmt.Sprintf("Inspecting worker: %s (Esc to exit)", selected), nil)
	case "memory":
		// First selection: project → show topics picker
		topics := listProjectTopics(selected)
		if len(topics) == 0 {
			m.addMsg("system", "", fmt.Sprintf("No memory found for project: %s", selected), nil)
		} else {
			m.pickerActive = true
			m.pickerTitle = fmt.Sprintf("Memory for %s:", selected)
			m.pickerOptions = topics
			m.pickerCursor = 0
			m.pickerAction = "memory-topic:" + selected
		}
	case "forget":
		if selected == "── all ──" {
			m.pendingConfirm = "forget-all"
			m.addMsg("system", "", "This will delete ALL project memory and the RAG index. Type 'yes' to confirm.", nil)
		} else {
			m.pendingConfirm = "forget:" + selected
			topics := listProjectTopics(selected)
			m.addMsg("system", "", fmt.Sprintf("Delete all memory for '%s' (%s)? Type 'yes' to confirm.", selected, strings.Join(topics, ", ")), nil)
		}
	default:
		// Handle memory-topic:projectName actions
		if strings.HasPrefix(action, "memory-topic:") {
			project := strings.TrimPrefix(action, "memory-topic:")
			content := readMemoryTopic(project, selected)
			m.addMsg("system", "", fmt.Sprintf("── %s / %s ──\n%s", project, selected, content), nil)
		}
	}
	return m
}
