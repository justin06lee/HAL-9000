# HAL 9000 Daemon Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split HAL 9000 from a monolithic TUI binary into a persistent daemon (`hal9000d`) and thin TUI client (`hal9000`), connected over a unix socket with newline-delimited JSON.

**Architecture:** The daemon owns all state (HAL conversation, orchestrator, memory, voice, session) and exposes it over `~/.config/hal9000/hal9000.sock`. The TUI client is a pure Bubble Tea rendering layer that mirrors daemon state. The orchestrator communicates via an `EventHandler` interface instead of channels; the daemon's socket server implements this interface to broadcast events to connected clients.

**Tech Stack:** Go 1.24+, Bubble Tea v2, `github.com/getlantern/systray` (menu bar), unix sockets (stdlib `net`), `osascript` (notifications), `launchctl` (launchd).

**Spec:** `docs/superpowers/specs/2026-03-25-daemon-architecture-design.md`

---

## File Structure

### New directory layout

```
HAL-9000/
├── cmd/
│   ├── hal9000d/
│   │   └── main.go              # daemon entry point (signal handling, install/uninstall)
│   └── hal9000/
│       └── main.go              # TUI client entry point
├── internal/
│   ├── config/
│   │   └── config.go            # exported constants, paths, prompts
│   ├── protocol/
│   │   └── messages.go          # all wire message types (shared)
│   ├── memory/
│   │   ├── memory.go            # topic storage, tag extraction, portfolio
│   │   └── rag.go               # TF-IDF vector search
│   ├── hal/
│   │   └── conversation.go      # HAL conversation + spec management
│   ├── orchestrator/
│   │   └── orchestrator.go      # worker lifecycle, manager flow, EventHandler
│   ├── scanner/
│   │   └── scanner.go           # repo analysis
│   ├── voice/
│   │   └── voice.go             # Piper TTS
│   ├── session/
│   │   └── session.go           # session persistence
│   ├── renderer/
│   │   └── eye.go               # 3D eye rendering
│   ├── daemon/
│   │   ├── server.go            # unix socket listener, client management, broadcast
│   │   ├── handler.go           # command handler, HAL response processing pipeline
│   │   ├── menubar.go           # systray menu bar icon + dropdown
│   │   └── notify.go            # macOS notifications
│   └── client/
│       ├── connection.go        # socket connect, auto-start daemon, message reader
│       ├── commands.go          # slash command parsing → protocol messages
│       ├── model.go             # Bubble Tea model (rendering + mirrored daemon state)
│       └── view.go              # View rendering
├── assets/
│   └── hal9000taskbar.png       # menu bar icon
├── docs/
├── go.mod
└── go.sum
```

### Files to delete after migration

All root-level `.go` files: `config.go`, `hal.go`, `orchestrator.go`, `model.go`, `view.go`, `commands.go`, `memory.go`, `rag.go`, `scanner.go`, `voice.go`, `session.go`, `renderer.go`, `main.go`.

---

## Task 1: Project Scaffolding + Protocol Package

**Files:**
- Create: `cmd/hal9000d/main.go` (stub)
- Create: `cmd/hal9000/main.go` (stub)
- Create: `internal/protocol/messages.go`
- Modify: `go.mod`

- [ ] **Step 1: Create directory structure**

```bash
mkdir -p cmd/hal9000d cmd/hal9000
mkdir -p internal/{config,protocol,memory,hal,orchestrator,scanner,voice,session,renderer,daemon,client}
mkdir -p assets
```

- [ ] **Step 2: Create stub entry points**

`cmd/hal9000d/main.go`:
```go
package main

func main() {
	// TODO: daemon entry point
}
```

`cmd/hal9000/main.go`:
```go
package main

func main() {
	// TODO: client entry point
}
```

- [ ] **Step 3: Define protocol message types**

`internal/protocol/messages.go` — all wire format types shared between daemon and client:

```go
package protocol

import "encoding/json"

const Version = 1

// ── Client → Daemon ─────────────────────────────────────────

type ClientMsg struct {
	Type     string `json:"type"`
	Protocol int    `json:"protocol,omitempty"`
	Text     string `json:"text,omitempty"`
	Name     string `json:"name,omitempty"`
	Arg      string `json:"arg,omitempty"`
	Worker   string `json:"worker,omitempty"`
	Answer   string `json:"answer,omitempty"`
}

// Client message type constants
const (
	MsgSubscribe        = "subscribe"
	MsgUnsubscribe      = "unsubscribe"
	MsgSendMessage      = "send_message"
	MsgCommand          = "command"
	MsgAnswerQuestion   = "answer_question"
	MsgStartBuild       = "start_build"
	MsgInterrupt        = "interrupt"
	MsgRequestWorkerLog = "request_worker_log"
)

// ── Daemon → Client ─────────────────────────────────────────
//
// Wire format is flat JSON per the spec: {"type": "...", ...fields...}
// We use json.RawMessage to marshal/unmarshal without nesting.

// Server message type constants
const (
	MsgStateSync        = "state_sync"
	MsgHALResponse      = "hal_response"
	MsgHALThinking      = "hal_thinking"
	MsgWorkerStatus     = "worker_status"
	MsgWorkerOutput     = "worker_output"
	MsgWorkerQuestion   = "worker_question"
	MsgQuestionResolved = "question_resolved"
	MsgPendingQuestions = "pending_questions"
	MsgSystemMessage    = "system_message"
	MsgModelPicker      = "model_picker"
	MsgConfirmPrompt    = "confirm_prompt"
	MsgError            = "error"
)

// MarshalServerMsg produces flat JSON: merges {"type": msgType} with the
// JSON representation of data. This matches the spec's wire format.
func MarshalServerMsg(msgType string, data interface{}) ([]byte, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	// For string data (system_message, error), wrap as {"text": "..."}
	if s, ok := data.(string); ok {
		payload, _ = json.Marshal(map[string]string{"text": s})
	}
	// Merge type field into the flat object
	if payload[0] == '{' {
		return append(append([]byte(`{"type":"`+msgType+`",`), payload[1:]...), nil), nil
	}
	// Fallback: wrap in data field
	return json.Marshal(map[string]interface{}{"type": msgType, "data": data})
}

// ParseServerMsg extracts the type field and returns the raw JSON for
// the caller to unmarshal into the appropriate type.
func ParseServerMsg(line []byte) (msgType string, raw json.RawMessage, err error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err = json.Unmarshal(line, &envelope); err != nil {
		return
	}
	return envelope.Type, json.RawMessage(line), nil
}

// ── Payload types ───────────────────────────────────────────

type ChatMessage struct {
	Role    string   `json:"role"`
	Name    string   `json:"name,omitempty"`
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
}

type WorkerInfo struct {
	Status     string `json:"status"`
	Activity   string `json:"activity"`
	SessionID  string `json:"session_id"`
	IsAudit    bool   `json:"is_audit"`
	ParentName string `json:"parent_name"`
	ErrMsg     string `json:"err_msg"`
}

type SpecInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoPath    string `json:"repo_path"`
}

type QuestionInfo struct {
	Name    string   `json:"name"`
	Text    string   `json:"text"`
	Options []string `json:"options"`
}

type StateSync struct {
	Protocol         int                    `json:"protocol"`
	Messages         []ChatMessage          `json:"messages"`
	Workers          map[string]*WorkerInfo `json:"workers"`
	Specs            []SpecInfo             `json:"specs"`
	PendingQuestions PendingQuestionsData   `json:"pending_questions"`
	HALThinking      bool                   `json:"hal_thinking"`
	BuildStarted     bool                   `json:"build_started"`
	Model            string                 `json:"model"`
	Effort           string                 `json:"effort"`
}

type PendingQuestionsData struct {
	Current *QuestionInfo  `json:"current"`
	Queued  []QuestionInfo `json:"queued"`
	Total   int            `json:"total"`
}

type HALResponse struct {
	Text       string `json:"text"`
	SpecText   string `json:"spec_text,omitempty"`
	StartBuild bool   `json:"start_build,omitempty"`
}

type HALThinking struct {
	Thinking bool `json:"thinking"`
}

type WorkerStatusUpdate struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Activity string `json:"activity,omitempty"`
}

type WorkerOutputData struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

type ModelPickerData struct {
	Models        []ModelEntry `json:"models"`
	Efforts       []string     `json:"efforts"`
	CurrentModel  string       `json:"current_model"`
	CurrentEffort string       `json:"current_effort"`
}

type ModelEntry struct {
	Display string `json:"display"`
	ID      string `json:"id"`
}

type ConfirmPromptData struct {
	Action string `json:"action"`
	Text   string `json:"text"`
}
```

- [ ] **Step 4: Add systray dependency to go.mod**

```bash
cd /Users/huiyunlee/Workspace/github.com/justin06lee/multivac-all/HAL-9000
go get github.com/getlantern/systray
```

- [ ] **Step 5: Verify the project compiles**

```bash
go build ./...
```

Expected: compiles with no errors (old root files still exist, stubs are trivial).

- [ ] **Step 6: Commit**

```bash
git add cmd/ internal/protocol/ assets/ go.mod go.sum
git commit -m "scaffold: create daemon/client directory structure and protocol types"
```

---

## Task 2: Config Package

Extract `config.go` into `internal/config/`. Key change: replace `os.Getwd()` with a configurable data directory defaulting to `~/.config/hal9000/data/`.

**Files:**
- Create: `internal/config/config.go`
- Reference: `config.go` (current, root-level)

- [ ] **Step 1: Write test for config initialization**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInit(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)

	if SpecsDir != filepath.Join(tmp, "specs") {
		t.Errorf("SpecsDir = %q, want %q", SpecsDir, filepath.Join(tmp, "specs"))
	}
	if MemoryDir != filepath.Join(tmp, "memory") {
		t.Errorf("MemoryDir = %q, want %q", MemoryDir, filepath.Join(tmp, "memory"))
	}
	if _, err := os.Stat(SpecsDir); os.IsNotExist(err) {
		t.Error("SpecsDir was not created")
	}
}

func TestDefaultDataDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".config", "hal9000", "data")
	if DefaultDataDir() != expected {
		t.Errorf("DefaultDataDir() = %q, want %q", DefaultDataDir(), expected)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/ -v
```

Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement config package**

`internal/config/config.go`:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	EyeFPS          = 18
	EyeCycleSeconds = 7.0
	ManagerTimeout  = 120
	QuestionMarker  = `"type": "question"`
	MaxMessages     = 500

	SocketName = "hal9000.sock"
	LogName    = "hal9000d.log"
)

// Centralized model and effort lists — single source of truth.
var ModelIDs = []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"}
var ModelDisplayNames = []string{"Opus 4.6", "Sonnet 4.6", "Haiku 4.5"}
var EffortLevels = []string{"low", "medium", "high", "max"}

var (
	ClaudeBin      string
	DataDir        string
	SpecsDir       string
	MemoryDir      string
	SessionFile    string
	VectorsDir     string
	IndexFile      string
	ClaudeModel    string
	ThinkingBudget string
	SocketPath     string
)

func DefaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "hal9000", "data")
}

func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "hal9000")
}

// Init sets up all path variables from the given data directory
// and creates required subdirectories.
func Init(dataDir string) {
	ClaudeBin = os.Getenv("CLAUDE_BIN")
	if ClaudeBin == "" {
		ClaudeBin = "claude"
	}

	DataDir = dataDir
	SpecsDir = filepath.Join(dataDir, "specs")
	MemoryDir = filepath.Join(dataDir, "memory")
	SessionFile = filepath.Join(dataDir, "session.json")
	VectorsDir = filepath.Join(dataDir, "vectors")
	IndexFile = filepath.Join(VectorsDir, "index.json")
	SocketPath = filepath.Join(filepath.Dir(dataDir), SocketName)

	for _, dir := range []string{SpecsDir, MemoryDir, VectorsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not create %s: %v\n", dir, err)
		}
	}
}

// ── System prompts ──────────────────────────────────────────

const ManagerSystemPrompt = `You are HAL 9000, an AI project manager orchestrating multiple software projects.
You have the full project specification below. A worker AI building one of these
projects has a question. Your job is to answer it AND rewrite your answer as a
clear, actionable directive that the worker can immediately act on.

Format your response as a direct instruction to the worker — not a discussion,
not options, but a clear "do this" statement. Include relevant context from the
spec so the worker doesn't need to re-derive it.

If the question requires the human's subjective input, a business decision, or
information that is genuinely not in the spec, reply with exactly: ESCALATE

Do not escalate if you can make a sound engineering judgment call.`

const WorkerPreamble = `You are a software engineer working on a project. You have full autonomy to build
the project as specified. If you encounter a decision point where multiple valid
approaches exist and the choice matters for the project's direction, or if you
need information not in your spec, emit a question in this exact JSON format on
its own line:

{"type": "question", "text": "<your question>", "options": ["Option A", "Option B", "Other (let me explain)"]}

Then STOP and wait for an answer. Do not proceed past a question until answered.
Continue building after receiving the answer.`

const HALSystemPrompt = `You are HAL 9000, an AI project orchestrator. You help the user define and manage
multiple software projects simultaneously. You speak in a calm, precise,
thoughtful manner — like the original HAL 9000 but helpful.

When the user describes a project they want to build:
1. Ask deep, thorough clarifying questions — one topic at a time. Cover: architecture,
   data models, API design, tech stack, authentication, error handling, deployment,
   testing strategy, edge cases, and performance requirements. Do NOT finalize a spec
   until you are confident you have comprehensive understanding. Keep asking until
   everything is crystal clear.
2. Once you have enough detail, confirm the spec with the user.
3. Output the final spec as a markdown document between <spec> and </spec> tags.
   Include: project name, description, tech stack, features, architecture notes,
   file structure, and any decisions made.

When you learn important information about a project during conversation, store it using
topic-based memory. Output: <memory project="project-name" topic="TOPIC">what you learned</memory>
where TOPIC is one of: overview, architecture, requirements, tech-stack, decisions, notes.
Choose the most appropriate topic. You can output multiple memory tags in one response.

When you first learn about a new project, also output a brief portfolio summary:
<portfolio project="project-name">1-2 line summary of what this project is</portfolio>
Update the portfolio whenever the project scope changes significantly.

You have access to a knowledge search system. Only the project portfolio (brief summaries) is
loaded by default to save tokens. When you need deeper information about a project's architecture,
requirements, decisions, etc., search your memory by outputting: <search query="your search query"/>
The system will automatically return relevant memory chunks and let you continue.
Use search when the user asks about project details not in the portfolio summary.

When the user wants to remove or discontinue a project, output:
<discontinue project="project-name"/>

When the user wants to start building, output: <start_build/>

You can manage projects naturally. The user may say things like "add a new project",
"remove that project", "tell me about project X", etc. Handle these gracefully.

When the user points you at an existing repository (you'll receive a repository analysis with
directory structure, config files, and source samples), study it carefully before asking
questions. Acknowledge what you see — the tech stack, architecture patterns, existing progress,
and current state. Then ask focused questions about what the user wants to do next: what to
change, add, fix, or build on top of what's already there. Do NOT ask the user to re-describe
things that are already evident from the code.

Every response MUST begin with a voice tag on the first line:
<voice>A calm 1-2 sentence spoken summary of what you're about to say</voice>

This voice line is read aloud to the user via text-to-speech. Keep it natural, brief, and
conversational — like HAL calmly narrating what comes next. Do not repeat the full content;
just give the gist so the user knows what to expect while reading.

Keep responses concise but thorough when gathering requirements. You are efficient and precise.`

const SecurityAuditPreamble = `You are a senior security auditor. Your job is to review the codebase in this
repository thoroughly and fix every issue you find.

Check for and fix:
- Hardcoded secrets, API keys, or credentials
- SQL injection, XSS, and other injection vulnerabilities
- Insecure dependencies or outdated packages
- Path traversal and file access issues
- Improper error handling that leaks information
- Authentication and authorization flaws
- Race conditions and concurrency issues
- Input validation gaps
- Insecure cryptographic practices

Fix every issue you find directly in the code. After fixing, create a brief summary.
If you find critical unfixable issues, start your final message with "CRITICAL:".
If everything passes review, start your final message with "PASS:".`
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: extract config package with configurable data directory"
```

---

## Task 3: Renderer + Voice + Scanner Packages

Three independent extractions with minimal dependencies. These are leaf packages — they don't import other internal packages.

**Files:**
- Create: `internal/renderer/eye.go`
- Create: `internal/voice/voice.go`
- Create: `internal/scanner/scanner.go`

- [ ] **Step 1: Write renderer test**

`internal/renderer/eye_test.go`:

```go
package renderer

import "testing"

func TestRenderEye(t *testing.T) {
	result := RenderEye(40, 10, 1.0, 7.0, 100.0)
	if result == "" {
		t.Error("RenderEye returned empty string")
	}
	if len(result) < 100 {
		t.Error("RenderEye output suspiciously short")
	}
}

func TestRenderEyeSmall(t *testing.T) {
	result := RenderEye(10, 5, 0.0, 7.0, 0.0)
	if result == "" {
		t.Error("RenderEye returned empty string for small dimensions")
	}
}
```

- [ ] **Step 2: Extract renderer package**

`internal/renderer/eye.go` — copy `renderer.go` contents, change `package main` to `package renderer`, export `RenderEye`. All helper functions (`vecNorm`, `dot3`, `rotY`, `rotX`, `lerpAngle`, `easeInOut`) stay unexported since they're internal to the package. Export the gaze constants so the client can reference them if needed.

Key changes from `renderer.go`:
- `package renderer`
- `renderEye` → `RenderEye`
- Everything else stays the same (pure math, no external deps)

- [ ] **Step 3: Run renderer test**

```bash
go test ./internal/renderer/ -v
```

Expected: PASS

- [ ] **Step 4: Write scanner test**

`internal/scanner/scanner_test.go`:

```go
package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanRepo(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc main() {}"), 0o644)
	os.WriteFile(filepath.Join(tmp, "README.md"), []byte("# Test"), 0o644)

	result, err := ScanRepo(tmp)
	if err != nil {
		t.Fatalf("ScanRepo failed: %v", err)
	}
	if !strings.Contains(result, "main.go") {
		t.Error("ScanRepo result should contain main.go")
	}
	if !strings.Contains(result, "# Test") {
		t.Error("ScanRepo result should contain README content")
	}
}

func TestResolvePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		input    string
		baseDir  string
		contains string
	}{
		{"~/test", "/base", filepath.Join(home, "test")},
		{"/absolute/path", "/base", "/absolute/path"},
		{"relative", "/base", filepath.Join("/base", "relative")},
	}
	for _, tt := range tests {
		result := ResolvePath(tt.input, tt.baseDir)
		if result != tt.contains {
			t.Errorf("ResolvePath(%q, %q) = %q, want %q", tt.input, tt.baseDir, result, tt.contains)
		}
	}
}
```

- [ ] **Step 5: Extract scanner package**

`internal/scanner/scanner.go` — copy `scanner.go`, change to `package scanner`. Key changes:
- `package scanner`
- `scanRepo` → `ScanRepo`
- `resolvePath(p string)` → `ResolvePath(p, baseDir string)` — takes baseDir as parameter instead of using global
- `detectRepoPath(text string)` → `DetectRepoPath(text, baseDir string)` — same change
- Remove `baseDir` global dependency; the caller passes it
- Export `KeyFiles` and `SourceExts` vars

- [ ] **Step 6: Run scanner test**

```bash
go test ./internal/scanner/ -v
```

Expected: PASS

- [ ] **Step 7: Extract voice package**

`internal/voice/voice.go` — copy `voice.go`, change to `package voice`. Key changes:
- `package voice`
- `halVoice` → `HALVoice`
- `newHALVoice` → `NewHALVoice`
- `sayAsync` → `SayAsync`
- `sayShort` → `SayShort`
- `acknowledge` → `Acknowledge`
- Helper functions (`fileExists`, `dirHasDylibs`, `downloadFile`, `downloadAndExtractTar`) stay unexported
- `acks` → `Acks` (exported for potential daemon use)

No test for voice (requires Piper/macOS `say` — tested manually).

- [ ] **Step 8: Verify all three packages compile**

```bash
go test ./internal/renderer/ ./internal/scanner/ -v
go build ./internal/voice/
```

Expected: tests pass, voice compiles.

- [ ] **Step 9: Commit**

```bash
git add internal/renderer/ internal/scanner/ internal/voice/
git commit -m "feat: extract renderer, scanner, and voice packages"
```

---

## Task 4: Memory Package

Extract `memory.go` and `rag.go` into `internal/memory/`. These share state (paths, tag regexes) and belong together.

**Files:**
- Create: `internal/memory/memory.go`
- Create: `internal/memory/rag.go`
- Create: `internal/memory/tags.go` (tag extraction + cleanDisplayText)

- [ ] **Step 1: Write memory test**

`internal/memory/memory_test.go`:

```go
package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadMemory(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp, filepath.Join(tmp, "vectors", "index.json"))
	os.MkdirAll(filepath.Join(tmp, "vectors"), 0o755)

	err := SaveMemory("test-project", "overview", "This is a test project")
	if err != nil {
		t.Fatalf("SaveMemory failed: %v", err)
	}

	content := LoadProjectMemory("test-project", "overview")
	if content == "" {
		t.Error("LoadProjectMemory returned empty")
	}

	topics := ListProjectTopics("test-project")
	if len(topics) != 1 || topics[0] != "overview" {
		t.Errorf("ListProjectTopics = %v, want [overview]", topics)
	}
}

func TestExtractAllMemories(t *testing.T) {
	text := `Some text <memory project="cool-api" topic="architecture">REST API with Go</memory> more text`
	entries := ExtractAllMemories(text)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Project != "cool-api" || entries[0].Topic != "architecture" {
		t.Errorf("got project=%q topic=%q", entries[0].Project, entries[0].Topic)
	}
}

func TestCleanDisplayText(t *testing.T) {
	text := `<voice>Hello</voice>

Some text <memory project="p" topic="t">content</memory>

<search query="test"/>

More text`
	clean := CleanDisplayText(text)
	if clean != "Some text\n\nMore text" {
		t.Errorf("CleanDisplayText = %q", clean)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/memory/ -v
```

Expected: FAIL

- [ ] **Step 3: Implement memory package**

`internal/memory/memory.go` — from current `memory.go`:
- `package memory`
- Add `Init(memDir, idxFile string)` to set package-level `memoryDir` and `indexFile` vars
- Export all public functions: `SaveMemory`, `LoadPortfolio`, `SavePortfolio`, `RemoveFromPortfolio`, `LoadProjectMemory`, `ListProjectTopics`, `ReadMemoryTopic`, `ListMemoryProjects`, `DeleteMemory`, `DeleteAllMemory`, `MigrateMemories`, `SanitizeProjectName`
- Export types: `MemoryEntry`, `PortfolioEntry`

`internal/memory/tags.go` — extract tag regexes and functions from `memory.go`:
- Export: `ExtractAllMemories`, `ExtractAllPortfolios`, `ExtractSearch`, `ExtractDiscontinue`, `ExtractVoice`, `StripSearchTags`, `CleanDisplayText`
- Move all regex vars (exported): `MemoryTagRe`, `PortfolioTagRe`, `SearchTagRe`, `DiscontinueTagRe`, `VoiceTagRe`, `SpecTagRe`, `StartBuildTagRe`

`internal/memory/rag.go` — from current `rag.go`:
- Export: `BuildIndex`, `Search`, `SaveIndex`, `LoadIndex`, `NeedsRebuild`, `RebuildIndex`, `RunRAGSearch`
- Export types: `Chunk`, `VectorIndex`, `SearchResult`
- Uses package-level `memoryDir` and `indexFile` set by `Init`

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/memory/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "feat: extract memory package with tag extraction and RAG search"
```

---

## Task 5: HAL Conversation Package

Extract `hal.go` into `internal/hal/`. The HAL conversation, spec management, and related types.

**Files:**
- Create: `internal/hal/conversation.go`

- [ ] **Step 1: Write test for spec parsing**

`internal/hal/conversation_test.go`:

```go
package hal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractSpec(t *testing.T) {
	c := &Conversation{}
	text := "Some text <spec>\n# My Project\nDetails here\n</spec> more text"
	spec := c.ExtractSpec(text)
	if spec != "# My Project\nDetails here" {
		t.Errorf("ExtractSpec = %q", spec)
	}
}

func TestCheckStartBuild(t *testing.T) {
	c := &Conversation{}
	if !c.CheckStartBuild("text <start_build/> more") {
		t.Error("should detect <start_build/>")
	}
	if !c.CheckStartBuild("text <start_build /> more") {
		t.Error("should detect <start_build />")
	}
	if c.CheckStartBuild("no tag here") {
		t.Error("should not detect when absent")
	}
}

func TestListSpecs(t *testing.T) {
	tmp := t.TempDir()
	content := "---\nname: test\ndescription: a test\nrepo_path: /tmp/test\n---\n\nSpec content"
	os.WriteFile(filepath.Join(tmp, "test.md"), []byte(content), 0o644)

	specs := ListSpecs(tmp)
	if len(specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(specs))
	}
	if specs[0].Name != "test" {
		t.Errorf("Name = %q, want 'test'", specs[0].Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/hal/ -v
```

Expected: FAIL

- [ ] **Step 3: Implement HAL package**

`internal/hal/conversation.go` — from current `hal.go`:
- `package hal`
- Export `Conversation` (from `halConversation`) with methods: `Send`, `Interrupt`, `GetSessionID`, `SetSessionID`, `ExtractSpec`, `CheckStartBuild`
- Add `SetSessionID(id string)` method for session restore:
  ```go
  func (h *Conversation) SetSessionID(id string) {
      h.mu.Lock()
      defer h.mu.Unlock()
      h.sessionID = id
  }
  ```
- Export `ProjectSpec` struct with all fields exported:
  ```go
  type ProjectSpec struct {
      Name        string
      Description string
      RepoPath    string
      SpecText    string
      FilePath    string
  }
  ```
- Export functions: `ListSpecs(specsDir string)`, `SaveSpec(specsDir, name, desc, repoPath, specText string)`, `BuildMasterSpec(specs []ProjectSpec)`
- `Conversation.Send` uses `config.ClaudeBin`, `config.ClaudeModel`, `config.ThinkingBudget`

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/hal/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hal/
git commit -m "feat: extract HAL conversation and spec management package"
```

---

## Task 6: Orchestrator Package

Extract `orchestrator.go` into `internal/orchestrator/`. Key change: replace channel-based communication with an `EventHandler` interface.

**Files:**
- Create: `internal/orchestrator/orchestrator.go`

- [ ] **Step 1: Write test for orchestrator**

`internal/orchestrator/orchestrator_test.go`:

```go
package orchestrator

import (
	"sync"
	"testing"
)

type mockHandler struct {
	mu       sync.Mutex
	statuses []string
	outputs  []string
}

func (m *mockHandler) OnWorkerStatus(name string, status WorkerStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses = append(m.statuses, name+":"+status.String())
}

func (m *mockHandler) OnWorkerOutput(name, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outputs = append(m.outputs, name+":"+text)
}

func (m *mockHandler) OnWorkerQuestion(name, text string, options []string) {}

func TestExtractActivity(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Creating database schema", "Creating database schema"},
		{"Running npm install", "Running npm install"},
		{"", ""},
		{"abc", ""},
		{"Random text that doesn't match", ""},
	}
	for _, tt := range tests {
		got := ExtractActivity(tt.input)
		if got != tt.want {
			t.Errorf("ExtractActivity(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAddAndGetWorker(t *testing.T) {
	handler := &mockHandler{}
	o := New(handler)
	defer o.Shutdown()

	o.AddWorker("test", "/tmp/test", "spec text")
	w := o.GetWorker("test")
	if w == nil {
		t.Fatal("GetWorker returned nil")
	}
	if w.Name != "test" {
		t.Errorf("Name = %q, want 'test'", w.Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/orchestrator/ -v
```

Expected: FAIL

- [ ] **Step 3: Implement orchestrator package**

`internal/orchestrator/orchestrator.go` — from current `orchestrator.go`:
- `package orchestrator`
- Define `EventHandler` interface:

```go
// EventHandler is implemented by the daemon to receive orchestrator events.
type EventHandler interface {
	OnWorkerStatus(name string, status WorkerStatus)
	OnWorkerOutput(name string, text string)
	OnWorkerQuestion(name string, text string, options []string)
}
```

- Export `WorkerStatus` (from `workerStatus`), all status constants: `StatusPending`, `StatusRunning`, etc.
- Export `WorkerState` with exported fields: `Name`, `RepoPath`, `Spec`, `Status`, `SessionID`, `ErrMsg`, `Question`, `OutputLog`, `LastActivity`, `IsAudit`, `ParentName`
- Export `WorkerQuestion` (from `workerQuestion`)
- Export `Orchestrator` type with `New(handler EventHandler)` constructor
- Remove channel fields (`statusCh`, `outputCh`, `questionCh`). Instead, the orchestrator calls `handler.OnWorkerStatus(...)`, `handler.OnWorkerOutput(...)`, `handler.OnWorkerQuestion(...)`
- Export methods: `Shutdown`, `AddWorker`, `GetWorker`, `StartAll`, `AnswerQuestion`, `AddAuditWorker`, `StartAuditWorker`, `RemoveWorker`, `WorkerNames`, `GetWorkerOutput`, `GetLastActivity`, `SetMasterSpec`, `RestoreWorker`
- `PrepareRepo` becomes exported
- `ExtractActivity` becomes exported (used by tests)
- Add `RestoreWorker` method for session restore (re-creates worker state without starting):
  ```go
  func (o *Orchestrator) RestoreWorker(name, repoPath, sessionID, statusStr string, isAudit bool) {
      o.mu.Lock()
      defer o.mu.Unlock()
      o.workers[name] = &WorkerState{
          Name:      name,
          RepoPath:  repoPath,
          SessionID: sessionID,
          Status:    StatusFromString(statusStr),
          IsAudit:   isAudit,
      }
  }
  ```
- Add `StatusFromString` to the orchestrator package (not session):
  ```go
  func StatusFromString(s string) WorkerStatus {
      switch s {
      case "pending":   return StatusPending
      case "running":   return StatusRunning
      case "waiting":   return StatusWaiting
      case "completed": return StatusCompleted
      case "auditing":  return StatusAuditing
      case "done":      return StatusDone
      case "failed":    return StatusFailed
      case "audit_failed": return StatusAuditFailed
      }
      return StatusPending
  }
  ```

Key structural change in `setStatus`:
```go
func (o *Orchestrator) setStatus(w *WorkerState, status WorkerStatus) {
	w.Mu.Lock()
	w.Status = status
	w.Mu.Unlock()
	o.handler.OnWorkerStatus(w.Name, status)
}
```

Similar for `emitOutput` → calls `o.handler.OnWorkerOutput(...)` and for question escalation → calls `o.handler.OnWorkerQuestion(...)`.

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/orchestrator/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/
git commit -m "feat: extract orchestrator with EventHandler interface replacing channels"
```

---

## Task 7: Session Package

Extract `session.go` into `internal/session/`. Key change: `saveSession` no longer takes `*model` — it takes a `SessionData` struct that the daemon populates.

**Files:**
- Create: `internal/session/session.go`

- [ ] **Step 1: Write test**

`internal/session/session_test.go`:

```go
package session

import (
	"path/filepath"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "session.json")

	sd := &Data{
		HALSessionID: "test-session-123",
		BuildStarted: true,
		Messages: []ChatMessageData{
			{Role: "hal", Text: "Hello"},
		},
	}

	Save(sd, path)
	loaded := Load(path)
	if loaded == nil {
		t.Fatal("Load returned nil")
	}
	if loaded.HALSessionID != "test-session-123" {
		t.Errorf("HALSessionID = %q", loaded.HALSessionID)
	}
	if !loaded.BuildStarted {
		t.Error("BuildStarted should be true")
	}
	if len(loaded.Messages) != 1 {
		t.Errorf("got %d messages, want 1", len(loaded.Messages))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/session/ -v
```

Expected: FAIL

- [ ] **Step 3: Implement session package**

`internal/session/session.go`:

```go
package session

import (
	"encoding/json"
	"os"
	"time"
)

type ChatMessageData struct {
	Role    string   `json:"role"`
	Name    string   `json:"name,omitempty"`
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
}

type Data struct {
	HALSessionID string               `json:"hal_session_id"`
	BuildStarted bool                 `json:"build_started"`
	Projects     []ProjectSessionData `json:"projects"`
	Workers      []WorkerSessionData  `json:"workers"`
	Messages     []ChatMessageData    `json:"messages,omitempty"`
	Model        string               `json:"model,omitempty"`
	Effort       string               `json:"effort,omitempty"`
	SavedAt      string               `json:"saved_at"`
}

type ProjectSessionData struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoPath    string `json:"repo_path"`
	SpecFile    string `json:"spec_file"`
}

type WorkerSessionData struct {
	Name      string `json:"name"`
	RepoPath  string `json:"repo_path"`
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
	IsAudit   bool   `json:"is_audit"`
}

func Save(sd *Data, path string) {
	sd.SavedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(sd, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0o644)
}

func Load(path string) *Data {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var sd Data
	if err := json.Unmarshal(data, &sd); err != nil {
		return nil
	}
	return &sd
}

// Note: StatusFromString lives in the orchestrator package (not here)
// to maintain type safety with WorkerStatus.
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/session/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/session/
git commit -m "feat: extract session persistence package"
```

---

## Task 8: Daemon Socket Server

The core daemon: unix socket listener, client connection management, and broadcast.

**Files:**
- Create: `internal/daemon/server.go`

- [ ] **Step 1: Write test for server**

`internal/daemon/server_test.go`:

```go
package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/justin06lee/HAL-9000/internal/protocol"
)

func TestServerAcceptsClient(t *testing.T) {
	tmp := t.TempDir()
	sockPath := filepath.Join(tmp, "test.sock")

	srv := NewServer(sockPath)
	go srv.Listen()
	defer srv.Close()

	// Wait for socket to appear
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// Send subscribe
	msg := protocol.ClientMsg{Type: protocol.MsgSubscribe, Protocol: protocol.Version}
	data, _ := json.Marshal(msg)
	conn.Write(append(data, '\n'))

	// Should receive state_sync
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatal("expected state_sync response")
	}

	var resp map[string]interface{}
	json.Unmarshal(scanner.Bytes(), &resp)
	if resp["type"] != protocol.MsgStateSync {
		t.Errorf("got type %q, want %q", resp["type"], protocol.MsgStateSync)
	}
}

func TestBroadcast(t *testing.T) {
	tmp := t.TempDir()
	sockPath := filepath.Join(tmp, "test.sock")

	srv := NewServer(sockPath)
	go srv.Listen()
	defer srv.Close()

	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Connect two clients
	c1, _ := net.Dial("unix", sockPath)
	defer c1.Close()
	c2, _ := net.Dial("unix", sockPath)
	defer c2.Close()

	// Subscribe both
	sub, _ := json.Marshal(protocol.ClientMsg{Type: protocol.MsgSubscribe, Protocol: protocol.Version})
	c1.Write(append(sub, '\n'))
	c2.Write(append(sub, '\n'))

	time.Sleep(100 * time.Millisecond)

	// Broadcast a system message
	srv.Broadcast(protocol.MsgSystemMessage, "Test broadcast")

	// Both should receive it
	for _, c := range []net.Conn{c1, c2} {
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		scanner := bufio.NewScanner(c)
		// Skip state_sync
		scanner.Scan()
		if scanner.Scan() {
			var msg map[string]interface{}
			json.Unmarshal(scanner.Bytes(), &msg)
			if msg["type"] != protocol.MsgSystemMessage {
				t.Errorf("got type %q, want system_message", msg["type"])
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/daemon/ -v
```

Expected: FAIL

- [ ] **Step 3: Implement server**

`internal/daemon/server.go`:

```go
package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/justin06lee/HAL-9000/internal/protocol"
)

type clientConn struct {
	conn       net.Conn
	writer     *bufio.Writer
	mu         sync.Mutex
	subscribed bool
	inspecting string // worker being inspected by this client
}

type Server struct {
	sockPath    string
	listener    net.Listener
	mu          sync.RWMutex
	clients     map[*clientConn]bool
	onMessage   func(*clientConn, protocol.ClientMsg) // handler callback
	stateFunc   func() *protocol.StateSync            // returns current state for sync
}

func NewServer(sockPath string) *Server {
	return &Server{
		sockPath: sockPath,
		clients:  make(map[*clientConn]bool),
	}
}

// SetOnMessage sets the callback for incoming client messages.
func (s *Server) SetOnMessage(fn func(*clientConn, protocol.ClientMsg)) {
	s.onMessage = fn
}

// SetStateFunc sets the function that returns current state for new clients.
func (s *Server) SetStateFunc(fn func() *protocol.StateSync) {
	s.stateFunc = fn
}

func (s *Server) Listen() error {
	// Remove stale socket
	if _, err := os.Stat(s.sockPath); err == nil {
		conn, err := net.Dial("unix", s.sockPath)
		if err == nil {
			conn.Close()
			return fmt.Errorf("daemon already running")
		}
		os.Remove(s.sockPath)
	}

	ln, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return err
	}
	s.listener = ln

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		cc := &clientConn{
			conn:   conn,
			writer: bufio.NewWriter(conn),
		}
		s.mu.Lock()
		s.clients[cc] = true
		s.mu.Unlock()
		go s.handleClient(cc)
	}
}

func (s *Server) handleClient(cc *clientConn) {
	defer func() {
		s.mu.Lock()
		delete(s.clients, cc)
		s.mu.Unlock()
		cc.conn.Close()
	}()

	scanner := bufio.NewScanner(cc.conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var msg protocol.ClientMsg
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case protocol.MsgSubscribe:
			if msg.Protocol != protocol.Version {
				s.sendTo(cc, protocol.MsgError, fmt.Sprintf(
					"protocol version mismatch: daemon=%d, client=%d",
					protocol.Version, msg.Protocol))
				return
			}
			cc.subscribed = true
			if s.stateFunc != nil {
				s.sendTo(cc, protocol.MsgStateSync, s.stateFunc())
			}
		case protocol.MsgUnsubscribe:
			return
		default:
			if s.onMessage != nil {
				s.onMessage(cc, msg)
			}
		}
	}
}

func (s *Server) sendTo(cc *clientConn, msgType string, data interface{}) {
	raw, err := protocol.MarshalServerMsg(msgType, data)
	if err != nil {
		return
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.writer.Write(raw)
	cc.writer.WriteByte('\n')
	cc.writer.Flush()
}

// Broadcast sends a message to all subscribed clients.
func (s *Server) Broadcast(msgType string, data interface{}) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for cc := range s.clients {
		if cc.subscribed {
			s.sendTo(cc, msgType, data)
		}
	}
}

// BroadcastToInspecting sends worker output only to clients inspecting that worker.
func (s *Server) BroadcastToInspecting(workerName string, data interface{}) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for cc := range s.clients {
		if cc.subscribed && cc.inspecting == workerName {
			s.sendTo(cc, protocol.MsgWorkerOutput, data)
		}
	}
}

// ConnectedClientCount returns the number of subscribed clients.
func (s *Server) ConnectedClientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for cc := range s.clients {
		if cc.subscribed {
			count++
		}
	}
	return count
}

// Close shuts down the server and removes the socket file.
func (s *Server) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.sockPath)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/daemon/ -v -timeout 10s
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/server.go internal/daemon/server_test.go
git commit -m "feat: implement daemon unix socket server with client management"
```

---

## Task 9: Daemon Command Handler + Event Loop

The daemon's brain: processes client commands, handles HAL response pipeline, implements `EventHandler`.

**Files:**
- Create: `internal/daemon/handler.go`

- [ ] **Step 1: Implement command handler**

`internal/daemon/handler.go` — this is the core daemon logic. It:
1. Implements `orchestrator.EventHandler` to receive worker events and broadcast them
2. Processes the HAL response pipeline (currently in `model.go` lines 717-773):
   - Extract voice → speak
   - Extract memories → save
   - Extract portfolios → update
   - Rebuild RAG async
   - Extract discontinue → remove project
   - Extract spec → save spec
   - Clean display text
   - Broadcast `hal_response`
3. Routes client commands to subsystems

```go
package daemon

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/justin06lee/HAL-9000/internal/config"
	"github.com/justin06lee/HAL-9000/internal/hal"
	"github.com/justin06lee/HAL-9000/internal/memory"
	"github.com/justin06lee/HAL-9000/internal/orchestrator"
	"github.com/justin06lee/HAL-9000/internal/protocol"
	"github.com/justin06lee/HAL-9000/internal/scanner"
	"github.com/justin06lee/HAL-9000/internal/session"
	"github.com/justin06lee/HAL-9000/internal/voice"
)

// Daemon is the central coordinator that owns all state.
type Daemon struct {
	mu sync.RWMutex

	server *Server
	conv   *hal.Conversation
	orch   *orchestrator.Orchestrator
	voice  *voice.HALVoice

	messages         []protocol.ChatMessage
	projectSpecs     []hal.ProjectSpec
	buildStarted     bool
	halThinking      bool
	currentQuestion  *protocol.QuestionInfo
	pendingQuestions  []protocol.QuestionInfo
	pendingConfirm   map[*clientConn]string // per-client confirm state
}

func NewDaemon(srv *Server) *Daemon {
	d := &Daemon{
		server:         srv,
		conv:           &hal.Conversation{},
		voice:          voice.NewHALVoice(),
		projectSpecs:   hal.ListSpecs(config.SpecsDir),
		pendingConfirm: make(map[*clientConn]string),
	}
	d.orch = orchestrator.New(d) // Daemon implements EventHandler
	srv.SetOnMessage(d.handleMessage)
	srv.SetStateFunc(d.buildStateSync)
	return d
}

// ── EventHandler implementation ─────────────────────────────

func (d *Daemon) OnWorkerStatus(name string, status orchestrator.WorkerStatus) {
	d.server.Broadcast(protocol.MsgWorkerStatus, protocol.WorkerStatusUpdate{
		Name:   name,
		Status: status.String(),
	})

	switch status {
	case orchestrator.StatusCompleted:
		w := d.orch.GetWorker(name)
		if w != nil && w.IsAudit {
			d.addSystemMsg(fmt.Sprintf("Security audit for '%s' passed.", w.ParentName))
			d.speak(w.ParentName + " audit complete.")
		} else if w != nil && !w.IsAudit {
			d.addSystemMsg(fmt.Sprintf("Worker '%s' completed. Starting security audit...", name))
			d.speak(name + " complete. Auditing.")
			d.orch.AddAuditWorker(name, w.RepoPath)
			d.orch.StartAuditWorker(name + " (audit)")
		}
	case orchestrator.StatusFailed:
		w := d.orch.GetWorker(name)
		if w != nil && w.IsAudit {
			d.addSystemMsg(fmt.Sprintf("Security audit FAILED for '%s'.", w.ParentName))
		} else if w != nil {
			d.addSystemMsg(fmt.Sprintf("Worker '%s' FAILED: %s", name, w.ErrMsg))
		}
		d.notifyIfNoClients(fmt.Sprintf("Worker '%s' failed", name))
	case orchestrator.StatusWaiting:
		d.addSystemMsg(fmt.Sprintf("Worker '%s' has a question...", name))
	}

	d.saveSessionAsync()
}

func (d *Daemon) OnWorkerOutput(name, text string) {
	d.server.BroadcastToInspecting(name, protocol.WorkerOutputData{
		Name: name, Text: text,
	})
}

func (d *Daemon) OnWorkerQuestion(name, text string, options []string) {
	q := protocol.QuestionInfo{Name: name, Text: text, Options: options}

	d.mu.Lock()
	if d.currentQuestion == nil {
		d.currentQuestion = &q
		d.addMsgLocked("question", name, text, options)
		d.mu.Unlock()
		d.speak("Question from " + name)
	} else {
		d.pendingQuestions = append(d.pendingQuestions, q)
		total := len(d.pendingQuestions) + 1
		d.mu.Unlock()
		d.speak(fmt.Sprintf("You have %d questions pending.", total))
		d.addSystemMsg(fmt.Sprintf("%d questions pending. Use /questions to review.", total))
	}

	d.server.Broadcast(protocol.MsgWorkerQuestion, q)
	d.notifyIfNoClients(fmt.Sprintf("Question from %s: %s", name, text))
}

// ── Client message handling ─────────────────────────────────

func (d *Daemon) handleMessage(cc *clientConn, msg protocol.ClientMsg) {
	switch msg.Type {
	case protocol.MsgSendMessage:
		d.handleSendMessage(cc, msg.Text)
	case protocol.MsgCommand:
		d.handleCommand(cc, msg.Name, msg.Arg)
	case protocol.MsgAnswerQuestion:
		d.handleAnswerQuestion(msg.Worker, msg.Answer)
	case protocol.MsgStartBuild:
		d.handleStartBuild()
	case protocol.MsgInterrupt:
		d.handleInterrupt()
	case protocol.MsgRequestWorkerLog:
		output := d.orch.GetWorkerOutput(msg.Worker)
		for _, line := range output {
			d.server.sendTo(cc, protocol.MsgWorkerOutput, protocol.WorkerOutputData{
				Name: msg.Worker, Text: line,
			})
		}
	}
}

func (d *Daemon) handleCommand(cc *clientConn, name, arg string) {
	switch name {
	case "scan":
		d.handleScan(arg)
	case "discontinue":
		d.handleDiscontinue(arg)
	case "inspect":
		cc.inspecting = arg
	case "inspect_stop":
		cc.inspecting = ""
	case "search":
		results := memory.RunRAGSearch(arg)
		d.server.sendTo(cc, protocol.MsgSystemMessage, results)
	case "memory":
		d.handleMemoryCommand(cc, arg)
	case "forget":
		d.handleForget(cc, arg)
	case "model":
		d.handleModel(cc, arg)
	case "new":
		d.handleNew()
	case "clear":
		d.handleClear()
	case "questions":
		d.handleQuestions(cc)
	}
}

func (d *Daemon) handleSendMessage(cc *clientConn, text string) {
	// Check for pending confirm
	d.mu.Lock()
	confirm, hasConfirm := d.pendingConfirm[cc]
	if hasConfirm {
		delete(d.pendingConfirm, cc)
	}
	d.mu.Unlock()

	if hasConfirm {
		d.processConfirm(confirm, text)
		return
	}

	// Check if answering current question
	d.mu.RLock()
	hasQuestion := d.currentQuestion != nil
	d.mu.RUnlock()

	if hasQuestion {
		d.addMsg("user", "", text, nil)
		d.handleAnswerQuestion(d.currentQuestion.Name, text)
		return
	}

	d.addMsg("user", "", text, nil)

	// Auto-detect repo paths
	if repoPath := scanner.DetectRepoPath(text, config.DataDir); repoPath != "" {
		d.setThinking(true)
		d.addSystemMsg(fmt.Sprintf("Detected repo path: %s — scanning...", repoPath))
		go d.scanAndSend(repoPath, text)
	} else {
		d.setThinking(true)
		go d.sendToHAL(text)
	}
}

func (d *Daemon) sendToHAL(text string) {
	response, err := d.conv.Send(text)
	if err != nil {
		d.setThinking(false)
		d.addSystemMsg("Error: " + err.Error())
		return
	}
	response = d.resolveSearchLoops(response)
	d.processHALResponse(response)
}

func (d *Daemon) scanAndSend(repoPath, userText string) {
	analysis, err := scanner.ScanRepo(repoPath)
	if err != nil {
		d.setThinking(false)
		d.addSystemMsg("Scan failed: " + err.Error())
		return
	}

	projName := strings.TrimRight(repoPath, "/")
	if i := strings.LastIndex(projName, "/"); i >= 0 {
		projName = projName[i+1:]
	}

	// Store scan data to memory
	memory.SaveMemory(projName, "overview", fmt.Sprintf("Repository at: %s\nScanned automatically.", repoPath))
	memory.SavePortfolio(projName, fmt.Sprintf("Project at %s (scanned)", repoPath))
	go memory.RebuildIndex()

	var prompt strings.Builder
	prompt.WriteString("I'm pointing you at an existing repository. Here's what's in it:\n\n")
	prompt.WriteString(analysis)
	prompt.WriteString("\n\nI've already stored all the scan data into structured memory.\n\n")
	if userText != "" {
		prompt.WriteString("User's message: ")
		prompt.WriteString(userText)
	} else {
		prompt.WriteString("Analyze this project comprehensively. Tell me what you see.")
	}

	response, err := d.conv.Send(prompt.String())
	if err != nil {
		d.setThinking(false)
		d.addSystemMsg("Error: " + err.Error())
		return
	}
	response = d.resolveSearchLoops(response)
	d.processHALResponse(response)
}

// processHALResponse is the response processing pipeline from the spec.
func (d *Daemon) processHALResponse(response string) {
	d.setThinking(false)

	// 1. Extract voice → speak
	voiceLine := memory.ExtractVoice(response)

	// 2. Extract memories → save
	for _, mem := range memory.ExtractAllMemories(response) {
		if err := memory.SaveMemory(mem.Project, mem.Topic, mem.Content); err == nil {
			d.addSystemMsg(fmt.Sprintf("Saved %s/%s to memory.", mem.Project, mem.Topic))
		}
	}

	// 3. Extract portfolios → update
	for _, port := range memory.ExtractAllPortfolios(response) {
		if err := memory.SavePortfolio(port.Project, port.Summary); err == nil {
			d.addSystemMsg(fmt.Sprintf("Updated portfolio: %s", port.Project))
		}
	}

	// 4. Rebuild RAG async if changed
	if len(memory.ExtractAllMemories(response)) > 0 || len(memory.ExtractAllPortfolios(response)) > 0 {
		go memory.RebuildIndex()
	}

	// 5. Extract discontinue
	if projName := memory.ExtractDiscontinue(response); projName != "" {
		d.handleDiscontinue(projName)
	}

	// 6. Extract spec
	specText := d.conv.ExtractSpec(response)

	// 7. Clean display text
	displayText := memory.CleanDisplayText(response)

	// 8. Speak (with firstSentence fallback if no voice tag)
	if voiceLine != "" {
		d.speak(voiceLine)
	} else if displayText != "" {
		d.speakShort(firstSentence(displayText))
	}

	// 9. Add to messages and broadcast
	d.addMsg("hal", "", displayText, nil)
	startBuild := d.conv.CheckStartBuild(response)

	d.server.Broadcast(protocol.MsgHALResponse, protocol.HALResponse{
		Text:       displayText,
		SpecText:   specText,
		StartBuild: startBuild,
	})

	if specText != "" {
		d.processNewSpec(specText)
	}
	if startBuild {
		d.handleStartBuild()
	}

	d.saveSessionAsync()
}

func (d *Daemon) resolveSearchLoops(response string) string {
	for i := 0; i < 3; i++ {
		query := memory.ExtractSearch(response)
		if query == "" {
			break
		}
		results := memory.RunRAGSearch(query)
		var err error
		response, err = d.conv.Send(results)
		if err != nil {
			break
		}
	}
	return memory.StripSearchTags(response)
}

// ── State helpers ───────────────────────────────────────────

func (d *Daemon) buildStateSync() *protocol.StateSync {
	d.mu.RLock()
	defer d.mu.RUnlock()

	workers := make(map[string]*protocol.WorkerInfo)
	for _, name := range d.orch.WorkerNames() {
		w := d.orch.GetWorker(name)
		if w != nil {
			w.Mu.Lock()
			workers[name] = &protocol.WorkerInfo{
				Status:     w.Status.String(),
				Activity:   w.LastActivity,
				SessionID:  w.SessionID,
				IsAudit:    w.IsAudit,
				ParentName: w.ParentName,
				ErrMsg:     w.ErrMsg,
			}
			w.Mu.Unlock()
		}
	}

	specs := make([]protocol.SpecInfo, len(d.projectSpecs))
	for i, s := range d.projectSpecs {
		specs[i] = protocol.SpecInfo{Name: s.Name, Description: s.Description, RepoPath: s.RepoPath}
	}

	pq := protocol.PendingQuestionsData{
		Current: d.currentQuestion,
		Queued:  d.pendingQuestions,
		Total:   len(d.pendingQuestions),
	}
	if d.currentQuestion != nil {
		pq.Total++
	}

	msgs := make([]protocol.ChatMessage, len(d.messages))
	copy(msgs, d.messages)

	return &protocol.StateSync{
		Protocol:         protocol.Version,
		Messages:         msgs,
		Workers:          workers,
		Specs:            specs,
		PendingQuestions:  pq,
		HALThinking:      d.halThinking,
		BuildStarted:     d.buildStarted,
		Model:            config.ClaudeModel,
		Effort:           config.ThinkingBudget,
	}
}

func (d *Daemon) addMsg(role, name, text string, options []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addMsgLocked(role, name, text, options)
}

func (d *Daemon) addMsgLocked(role, name, text string, options []string) {
	d.messages = append(d.messages, protocol.ChatMessage{
		Role: role, Name: name, Text: text, Options: options,
	})
	if len(d.messages) > config.MaxMessages {
		d.messages = d.messages[len(d.messages)-config.MaxMessages:]
	}
}

func (d *Daemon) addSystemMsg(text string) {
	d.addMsg("system", "", text, nil)
	d.server.Broadcast(protocol.MsgSystemMessage, text)
}

func (d *Daemon) setThinking(v bool) {
	d.mu.Lock()
	d.halThinking = v
	d.mu.Unlock()
	d.server.Broadcast(protocol.MsgHALThinking, protocol.HALThinking{Thinking: v})
}

func (d *Daemon) speak(text string) {
	if d.server.ConnectedClientCount() > 0 {
		d.voice.SayAsync(text)
	}
}

func (d *Daemon) speakShort(text string) {
	if d.server.ConnectedClientCount() > 0 {
		d.voice.SayShort(text)
	}
}

func firstSentence(text string) string {
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

func (d *Daemon) notifyIfNoClients(text string) {
	if d.server.ConnectedClientCount() == 0 {
		SendNotification("HAL 9000", text)
	}
}

func (d *Daemon) handleStartBuild() {
	d.mu.Lock()
	if d.buildStarted {
		d.mu.Unlock()
		return
	}
	d.buildStarted = true
	specs := d.projectSpecs
	d.mu.Unlock()

	if len(specs) == 0 {
		d.addSystemMsg("No project specs defined yet.")
		return
	}

	d.orch.SetMasterSpec(hal.BuildMasterSpec(specs))
	d.addMsg("hal", "", fmt.Sprintf("Initiating build sequence for %d project(s).", len(specs)), nil)
	d.speak("Initiating build sequence.")

	for i := range specs {
		spec := &specs[i]
		spec.RepoPath = orchestrator.PrepareRepo(spec.RepoPath, spec.Name)
		d.orch.AddWorker(spec.Name, spec.RepoPath, spec.SpecText)
	}
	d.orch.StartAll()
	d.saveSessionAsync()
}

func (d *Daemon) handleInterrupt() {
	d.conv.Interrupt()
	d.setThinking(false)
}

func (d *Daemon) handleNew() {
	d.mu.Lock()
	d.conv = &hal.Conversation{}
	d.messages = nil
	d.halThinking = false
	d.currentQuestion = nil
	d.pendingQuestions = nil
	d.mu.Unlock()

	greeting := timeGreeting()
	d.addMsg("hal", "", greeting+" Fresh session started. Your projects and workers are still here.", nil)
	d.saveSessionAsync()

	// Send fresh state to all clients
	if d.server.stateFunc != nil {
		state := d.server.stateFunc()
		d.server.Broadcast(protocol.MsgStateSync, state)
	}
}

func (d *Daemon) handleClear() {
	d.mu.Lock()
	d.messages = nil
	d.mu.Unlock()
	if d.server.stateFunc != nil {
		d.server.Broadcast(protocol.MsgStateSync, d.server.stateFunc())
	}
}

func (d *Daemon) handleAnswerQuestion(workerName, answer string) {
	d.mu.Lock()
	q := d.currentQuestion
	if q == nil {
		d.mu.Unlock()
		return
	}
	d.currentQuestion = nil
	if len(d.pendingQuestions) > 0 {
		next := d.pendingQuestions[0]
		d.pendingQuestions = d.pendingQuestions[1:]
		d.currentQuestion = &next
	}
	d.mu.Unlock()

	d.addSystemMsg(fmt.Sprintf("Answering %s: %s", q.Name, answer))
	d.orch.AnswerQuestion(q.Name, answer)
	d.server.Broadcast(protocol.MsgQuestionResolved, map[string]string{"worker": q.Name})

	if d.currentQuestion != nil {
		d.server.Broadcast(protocol.MsgWorkerQuestion, *d.currentQuestion)
	}
}

func (d *Daemon) handleScan(arg string) {
	if arg == "" {
		d.addSystemMsg("Usage: /scan <path-to-repo>")
		return
	}
	scanPath := scanner.ResolvePath(arg, config.DataDir)
	d.setThinking(true)
	d.addSystemMsg(fmt.Sprintf("Scanning repository: %s ...", scanPath))
	go d.scanAndSend(scanPath, "")
}

func (d *Daemon) handleDiscontinue(name string) {
	d.mu.Lock()
	found := -1
	for i, p := range d.projectSpecs {
		if strings.EqualFold(p.Name, name) {
			found = i
			break
		}
	}
	if found < 0 {
		d.mu.Unlock()
		d.addSystemMsg(fmt.Sprintf("Project '%s' not found.", name))
		return
	}
	spec := d.projectSpecs[found]
	d.projectSpecs = append(d.projectSpecs[:found], d.projectSpecs[found+1:]...)
	d.mu.Unlock()

	d.orch.RemoveWorker(spec.Name)
	d.orch.RemoveWorker(spec.Name + " (audit)")
	// Remove spec file from disk (but not the repo directory)
	if spec.FilePath != "" {
		os.Remove(spec.FilePath)
	}
	memory.DeleteMemory(spec.Name)
	d.addSystemMsg(fmt.Sprintf("Discontinued project '%s'.", spec.Name))
	d.speak("Project discontinued.")
	d.saveSessionAsync()
}

func (d *Daemon) handleModel(cc *clientConn, arg string) {
	if arg != "" {
		// arg can be "model_id" or "model_id:effort"
		parts := strings.SplitN(arg, ":", 2)
		config.ClaudeModel = parts[0]
		if len(parts) > 1 {
			if parts[1] == "medium" {
				config.ThinkingBudget = ""
			} else {
				config.ThinkingBudget = parts[1]
			}
		}
		d.addSystemMsg(fmt.Sprintf("Model: %s, Effort: %s", config.ClaudeModel, config.ThinkingBudget))
		d.saveSessionAsync()
	} else {
		d.server.sendTo(cc, protocol.MsgModelPicker, protocol.ModelPickerData{
			Models: []protocol.ModelEntry{
				{Display: "Opus 4.6", ID: "claude-opus-4-6"},
				{Display: "Sonnet 4.6", ID: "claude-sonnet-4-6"},
				{Display: "Haiku 4.5", ID: "claude-haiku-4-5-20251001"},
			},
			Efforts:       []string{"low", "medium", "high", "max"},
			CurrentModel:  config.ClaudeModel,
			CurrentEffort: config.ThinkingBudget,
		})
	}
}

func (d *Daemon) handleMemoryCommand(cc *clientConn, arg string) {
	if arg != "" {
		topics := memory.ListProjectTopics(arg)
		if len(topics) == 0 {
			d.server.sendTo(cc, protocol.MsgSystemMessage, fmt.Sprintf("No memory found for: %s", arg))
		} else {
			d.server.sendTo(cc, protocol.MsgSystemMessage, fmt.Sprintf("Memory topics for %s: %s", arg, strings.Join(topics, ", ")))
		}
	} else {
		names := memory.ListMemoryProjects()
		if len(names) == 0 {
			d.server.sendTo(cc, protocol.MsgSystemMessage, "No project memory stored.")
		} else {
			d.server.sendTo(cc, protocol.MsgSystemMessage, fmt.Sprintf("Projects with memory: %s", strings.Join(names, ", ")))
		}
	}
}

func (d *Daemon) handleForget(cc *clientConn, arg string) {
	if arg == "" {
		d.server.sendTo(cc, protocol.MsgSystemMessage, "Usage: /forget <project> or /forget all")
		return
	}
	if strings.ToLower(arg) == "all" {
		d.mu.Lock()
		d.pendingConfirm[cc] = "forget-all"
		d.mu.Unlock()
		d.server.sendTo(cc, protocol.MsgConfirmPrompt, protocol.ConfirmPromptData{
			Action: "forget-all",
			Text:   "This will delete ALL project memory and the RAG index. Type 'yes' to confirm.",
		})
	} else {
		d.mu.Lock()
		d.pendingConfirm[cc] = "forget:" + arg
		d.mu.Unlock()
		d.server.sendTo(cc, protocol.MsgConfirmPrompt, protocol.ConfirmPromptData{
			Action: "forget:" + arg,
			Text:   fmt.Sprintf("Delete all memory for '%s'? Type 'yes' to confirm.", arg),
		})
	}
}

func (d *Daemon) handleQuestions(cc *clientConn) {
	d.mu.RLock()
	pq := protocol.PendingQuestionsData{
		Current: d.currentQuestion,
		Queued:  d.pendingQuestions,
		Total:   len(d.pendingQuestions),
	}
	if d.currentQuestion != nil {
		pq.Total++
	}
	d.mu.RUnlock()
	d.server.sendTo(cc, protocol.MsgPendingQuestions, pq)
}

func (d *Daemon) processConfirm(action, text string) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "yes" || lower == "y" {
		if action == "forget-all" {
			memory.DeleteAllMemory()
			go memory.RebuildIndex()
			d.addSystemMsg("All memory wiped.")
		} else if strings.HasPrefix(action, "forget:") {
			proj := action[7:]
			memory.DeleteMemory(proj)
			go memory.RebuildIndex()
			d.addSystemMsg(fmt.Sprintf("Memory for '%s' deleted.", proj))
		}
	} else {
		d.addSystemMsg("Cancelled.")
	}
}

func (d *Daemon) processNewSpec(specText string) {
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

	repoPath := config.DataDir + "/" + name // simplified; use SafeName logic from hal package
	filePath, err := hal.SaveSpec(config.SpecsDir, name, desc, repoPath, specText)
	if err != nil {
		d.addSystemMsg(fmt.Sprintf("Failed to save spec: %v", err))
		return
	}

	d.mu.Lock()
	d.projectSpecs = append(d.projectSpecs, hal.ProjectSpec{
		Name: name, Description: desc, RepoPath: repoPath,
		SpecText: specText, FilePath: filePath,
	})
	d.mu.Unlock()

	d.addSystemMsg(fmt.Sprintf("Saved spec: %s -> %s", name, filePath))
	d.saveSessionAsync()
}

func (d *Daemon) saveSessionAsync() {
	go func() {
		d.mu.RLock()
		sd := &session.Data{
			HALSessionID: d.conv.GetSessionID(),
			BuildStarted: d.buildStarted,
			Model:        config.ClaudeModel,
			Effort:       config.ThinkingBudget,
		}
		msgs := d.messages
		if len(msgs) > config.MaxMessages {
			msgs = msgs[len(msgs)-config.MaxMessages:]
		}
		for _, m := range msgs {
			sd.Messages = append(sd.Messages, session.ChatMessageData{
				Role: m.Role, Name: m.Name, Text: m.Text, Options: m.Options,
			})
		}
		for _, p := range d.projectSpecs {
			sd.Projects = append(sd.Projects, session.ProjectSessionData{
				Name: p.Name, Description: p.Description,
				RepoPath: p.RepoPath, SpecFile: p.FilePath,
			})
		}
		d.mu.RUnlock()

		for _, name := range d.orch.WorkerNames() {
			w := d.orch.GetWorker(name)
			if w != nil {
				w.Mu.Lock()
				sd.Workers = append(sd.Workers, session.WorkerSessionData{
					Name: w.Name, RepoPath: w.RepoPath,
					Status: w.Status.String(), SessionID: w.SessionID,
					IsAudit: w.IsAudit,
				})
				w.Mu.Unlock()
			}
		}

		session.Save(sd, config.SessionFile)
	}()
}

// RestoreSession loads a saved session and restores daemon state.
func (d *Daemon) RestoreSession() {
	sd := session.Load(config.SessionFile)
	if sd == nil {
		greeting := timeGreeting()
		d.addMsg("hal", "", greeting+" I am HAL 9000. Tell me about the projects you would like to build.", nil)
		d.speak(greeting + " I am HAL 9000.")
		return
	}

	d.conv.SetSessionID(sd.HALSessionID)
	d.buildStarted = sd.BuildStarted
	if sd.Model != "" {
		config.ClaudeModel = sd.Model
	}
	if sd.Effort != "" {
		config.ThinkingBudget = sd.Effort
	}

	for _, msg := range sd.Messages {
		d.messages = append(d.messages, protocol.ChatMessage{
			Role: msg.Role, Name: msg.Name, Text: msg.Text, Options: msg.Options,
		})
	}

	for _, ws := range sd.Workers {
		d.orch.RestoreWorker(ws.Name, ws.RepoPath, ws.SessionID, ws.Status, ws.IsAudit)
	}

	d.speak(timeGreeting())
}

// Shutdown gracefully stops the daemon.
func (d *Daemon) Shutdown() {
	d.saveSessionAsync()
	d.orch.Shutdown()
	d.server.Close()
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
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/daemon/
```

Expected: compiles (may need to adjust imports as packages are finalized).

- [ ] **Step 3: Commit**

```bash
git add internal/daemon/handler.go
git commit -m "feat: implement daemon command handler and event loop"
```

---

## Task 10: Daemon Menu Bar + Notifications

**Files:**
- Create: `internal/daemon/menubar.go`
- Create: `internal/daemon/notify.go`

- [ ] **Step 1: Implement notifications**

`internal/daemon/notify.go`:

```go
package daemon

import (
	"os/exec"
	"runtime"
	"strings"
)

// SendNotification sends a macOS notification via osascript.
func SendNotification(title, text string) {
	if runtime.GOOS != "darwin" {
		return
	}
	// Escape double quotes to prevent osascript injection
	title = strings.ReplaceAll(title, `"`, `\"`)
	text = strings.ReplaceAll(text, `"`, `\"`)
	script := `display notification "` + text + `" with title "` + title + `"`
	exec.Command("osascript", "-e", script).Run()
}
```

- [ ] **Step 2: Implement menu bar**

`internal/daemon/menubar.go`:

```go
package daemon

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"github.com/getlantern/systray"
)

// Icon data is loaded at runtime by the daemon entry point via os.ReadFile,
// NOT via go:embed (which is relative to the package dir, not module root).

var (
	menuWorkerItems []*systray.MenuItem
	menuQuestions   *systray.MenuItem
	menuStop        *systray.MenuItem
)

// RunMenuBar starts the systray menu bar. Must be called from main goroutine.
// onStop is called when user clicks "Stop HAL".
func RunMenuBar(iconData []byte, d *Daemon, onStop func()) {
	systray.Run(func() {
		setupMenuBar(iconData, d, onStop)
	}, func() {})
}

func setupMenuBar(iconData []byte, d *Daemon, onStop func()) {
	systray.SetIcon(iconData)
	systray.SetTitle("")
	systray.SetTooltip("HAL 9000")

	menuQuestions = systray.AddMenuItem("No pending questions", "")
	menuQuestions.Disable()
	systray.AddSeparator()
	menuStop = systray.AddMenuItem("Stop HAL", "Shut down HAL 9000 daemon")

	go func() {
		for range menuStop.ClickedCh {
			onStop()
			systray.Quit()
		}
	}()
}

// UpdateMenuBar refreshes worker statuses and question count.
func UpdateMenuBar(d *Daemon) {
	d.mu.RLock()
	qCount := len(d.pendingQuestions)
	if d.currentQuestion != nil {
		qCount++
	}
	d.mu.RUnlock()

	if qCount > 0 {
		menuQuestions.SetTitle(fmt.Sprintf("%d question(s) pending", qCount))
	} else {
		menuQuestions.SetTitle("No pending questions")
	}
}

// composeBadgeIcon overlays a red dot on the icon for pending notifications.
func composeBadgeIcon(original []byte) []byte {
	img, _, err := image.Decode(bytes.NewReader(original))
	if err != nil {
		return original
	}
	bounds := img.Bounds()
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, img, bounds.Min, draw.Src)

	// Draw a red dot in the top-right corner (roughly 20% of icon size)
	dotR := bounds.Dx() / 5
	cx := bounds.Max.X - dotR - 2
	cy := bounds.Min.Y + dotR + 2
	red := color.RGBA{R: 255, G: 40, B: 40, A: 255}
	for y := cy - dotR; y <= cy+dotR; y++ {
		for x := cx - dotR; x <= cx+dotR; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= dotR*dotR {
				dst.Set(x, y, red)
			}
		}
	}

	var buf bytes.Buffer
	png.Encode(&buf, dst)
	return buf.Bytes()
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/daemon/menubar.go internal/daemon/notify.go
git commit -m "feat: add macOS menu bar and notification support"
```

---

## Task 11: Daemon Entry Point

**Files:**
- Create: `cmd/hal9000d/main.go` (replace stub)

- [ ] **Step 1: Implement daemon main**

`cmd/hal9000d/main.go`:

```go
package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/justin06lee/HAL-9000/internal/config"
	"github.com/justin06lee/HAL-9000/internal/daemon"
	"github.com/justin06lee/HAL-9000/internal/memory"
)

const version = "0.3.0"

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "install":
			install()
			return
		case "uninstall":
			uninstall()
			return
		case "stop":
			stop()
			return
		case "version", "--version", "-v":
			fmt.Printf("hal9000d v%s\n", version)
			return
		case "help", "--help", "-h":
			fmt.Println("HAL 9000 Daemon")
			fmt.Printf("Version: %s\n\n", version)
			fmt.Println("Usage: hal9000d [command]")
			fmt.Println("")
			fmt.Println("Commands:")
			fmt.Println("  install     Install as launchd service (starts on boot)")
			fmt.Println("  uninstall   Remove launchd service")
			fmt.Println("  stop        Stop running daemon")
			fmt.Println("  version     Show version")
			fmt.Println("")
			fmt.Println("Options:")
			fmt.Println("  --data-dir <path>   Override data directory")
			return
		}
	}

	// Parse optional --data-dir
	dataDir := config.DefaultDataDir()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--data-dir" {
			dataDir = args[i+1]
		}
	}

	config.Init(dataDir)
	memory.Init(config.MemoryDir, config.IndexFile)
	memory.MigrateMemories()

	srv := daemon.NewServer(config.SocketPath)
	d := daemon.NewDaemon(srv)
	d.RestoreSession()

	// Signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		d.Shutdown()
		os.Exit(0)
	}()

	// Load menu bar icon
	iconData, _ := os.ReadFile(findIcon())

	// systray.Run blocks on the main goroutine (macOS requirement).
	// Socket server runs in a goroutine.
	go func() {
		if err := srv.Listen(); err != nil {
			fmt.Fprintf(os.Stderr, "socket server: %v\n", err)
			os.Exit(1)
		}
	}()

	daemon.RunMenuBar(iconData, d, func() {
		d.Shutdown()
	})
}

func findIcon() string {
	// Check repo assets first, then fallback locations
	candidates := []string{
		"assets/hal9000taskbar.png",
		filepath.Join(config.DefaultConfigDir(), "hal9000taskbar.png"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

const plistLabel = "com.hal9000.daemon"

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
}

func install() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not find executable path: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.Abs(exe)
	logPath := filepath.Join(config.DefaultConfigDir(), config.LogName)

	os.MkdirAll(filepath.Dir(plistPath()), 0o755)
	os.MkdirAll(filepath.Dir(logPath), 0o755)

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://plist.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>`, plistLabel, exe, logPath, logPath)

	if err := os.WriteFile(plistPath(), []byte(plist), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write plist: %v\n", err)
		os.Exit(1)
	}

	cmd := exec.Command("launchctl", "load", plistPath())
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "launchctl load failed: %s\n", strings.TrimSpace(string(out)))
		os.Exit(1)
	}

	fmt.Println("HAL 9000 daemon installed and started.")
	fmt.Printf("Plist: %s\n", plistPath())
	fmt.Printf("Log:   %s\n", logPath)
}

func uninstall() {
	cmd := exec.Command("launchctl", "unload", plistPath())
	cmd.Run() // ignore error if not loaded
	os.Remove(plistPath())
	fmt.Println("HAL 9000 daemon uninstalled.")
}

func stop() {
	// launchctl stop + KeepAlive=true would restart immediately, so
	// we unload temporarily instead. This stops the daemon without
	// removing the plist (it will reload on next login).
	sockPath := filepath.Join(config.DefaultConfigDir(), config.SocketName)

	// First try connecting to socket to trigger graceful shutdown
	conn, err := net.Dial("unix", sockPath)
	if err == nil {
		// Send unsubscribe to trigger close (daemon handles SIGTERM via signal handler)
		conn.Close()
	}

	// Unload from launchd to actually stop (KeepAlive would restart after launchctl stop)
	cmd := exec.Command("launchctl", "unload", plistPath())
	cmd.Run()

	// Re-load so it starts on next boot
	cmd = exec.Command("launchctl", "load", plistPath())
	cmd.Run()

	fmt.Println("HAL 9000 daemon stopped.")
}
```

- [ ] **Step 2: Build daemon binary**

```bash
go build -o hal9000d ./cmd/hal9000d/
```

Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add cmd/hal9000d/main.go
git commit -m "feat: implement daemon entry point with install/uninstall and signal handling"
```

---

## Task 12: TUI Client Connection

**Files:**
- Create: `internal/client/connection.go`

- [ ] **Step 1: Implement socket connection**

`internal/client/connection.go`:

```go
package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/justin06lee/HAL-9000/internal/protocol"
)

// Connection manages the socket connection to the daemon.
type Connection struct {
	conn    net.Conn
	scanner *bufio.Scanner
	writer  *bufio.Writer
}

// Connect connects to the daemon socket, auto-starting if needed.
func Connect(sockPath string) (*Connection, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		// Auto-start daemon
		cmd := exec.Command("hal9000d")
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to start daemon: %w", err)
		}
		cmd.Process.Release()

		// Poll for socket
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			conn, err = net.Dial("unix", sockPath)
			if err == nil {
				break
			}
		}
		if err != nil {
			return nil, fmt.Errorf("daemon did not start within 5 seconds")
		}
	}

	c := &Connection{
		conn:    conn,
		scanner: bufio.NewScanner(conn),
		writer:  bufio.NewWriter(conn),
	}
	c.scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	return c, nil
}

// Subscribe sends the subscribe message and returns the initial state sync.
func (c *Connection) Subscribe() (*protocol.StateSync, error) {
	msg := protocol.ClientMsg{Type: protocol.MsgSubscribe, Protocol: protocol.Version}
	if err := c.Send(msg); err != nil {
		return nil, err
	}

	// Read state_sync response (flat JSON per spec)
	if !c.scanner.Scan() {
		return nil, fmt.Errorf("connection closed before state_sync")
	}

	msgType, raw, err := protocol.ParseServerMsg(c.scanner.Bytes())
	if err != nil {
		return nil, err
	}

	if msgType == protocol.MsgError {
		var errMsg struct{ Text string `json:"text"` }
		json.Unmarshal(raw, &errMsg)
		return nil, fmt.Errorf("daemon: %s", errMsg.Text)
	}

	if msgType != protocol.MsgStateSync {
		return nil, fmt.Errorf("expected state_sync, got %s", msgType)
	}

	var state protocol.StateSync
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// Send sends a client message to the daemon.
func (c *Connection) Send(msg protocol.ClientMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writer.Write(data)
	c.writer.WriteByte('\n')
	return c.writer.Flush()
}

// Close sends unsubscribe and closes the connection.
func (c *Connection) Close() {
	c.Send(protocol.ClientMsg{Type: protocol.MsgUnsubscribe})
	c.conn.Close()
}

// ── Bubble Tea integration ──────────────────────────────────

// ServerMsgReceived wraps a daemon message as a Bubble Tea Msg.
type ServerMsgReceived struct {
	Type string
	Raw  json.RawMessage
}

// ReadMessages returns a tea.Cmd that reads the next daemon message.
func (c *Connection) ReadMessages() tea.Cmd {
	return func() tea.Msg {
		if !c.scanner.Scan() {
			return ServerMsgReceived{Type: "disconnected"}
		}
		msgType, raw, err := protocol.ParseServerMsg(c.scanner.Bytes())
		if err != nil {
			return nil
		}
		return ServerMsgReceived{Type: msgType, Raw: raw}
	}
}

// SendCommand is a convenience for sending slash commands.
func (c *Connection) SendCommand(name, arg string) error {
	return c.Send(protocol.ClientMsg{
		Type: protocol.MsgCommand,
		Name: name,
		Arg:  arg,
	})
}

// SendText sends a chat message.
func (c *Connection) SendText(text string) error {
	return c.Send(protocol.ClientMsg{
		Type: protocol.MsgSendMessage,
		Text: text,
	})
}

// SendAnswer answers a worker question.
func (c *Connection) SendAnswer(worker, answer string) error {
	return c.Send(protocol.ClientMsg{
		Type:   protocol.MsgAnswerQuestion,
		Worker: worker,
		Answer: answer,
	})
}

// SendStartBuild sends the build command.
func (c *Connection) SendStartBuild() error {
	return c.Send(protocol.ClientMsg{Type: protocol.MsgStartBuild})
}

// SendInterrupt sends an interrupt.
func (c *Connection) SendInterrupt() error {
	return c.Send(protocol.ClientMsg{Type: protocol.MsgInterrupt})
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/client/connection.go
git commit -m "feat: implement TUI client socket connection with auto-start"
```

---

## Task 13: TUI Client Commands

**Files:**
- Create: `internal/client/commands.go`

- [ ] **Step 1: Write test**

`internal/client/commands_test.go`:

```go
package client

import "testing"

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input string
		name  string
		arg   string
		isNil bool
	}{
		{"/scan /path/to/repo", "scan", "/path/to/repo", false},
		{"/model opus", "model", "opus", false},
		{"/clear", "clear", "", false},
		{"/q", "questions", "", false},
		{"not a command", "", "", true},
	}
	for _, tt := range tests {
		cmd := ParseCommand(tt.input)
		if tt.isNil {
			if cmd != nil {
				t.Errorf("ParseCommand(%q) should be nil", tt.input)
			}
			continue
		}
		if cmd == nil {
			t.Errorf("ParseCommand(%q) returned nil", tt.input)
			continue
		}
		if cmd.Name != tt.name {
			t.Errorf("ParseCommand(%q).Name = %q, want %q", tt.input, cmd.Name, tt.name)
		}
		if cmd.Arg != tt.arg {
			t.Errorf("ParseCommand(%q).Arg = %q, want %q", tt.input, cmd.Arg, tt.arg)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/client/ -run TestParseCommand -v
```

Expected: FAIL

- [ ] **Step 3: Implement commands**

`internal/client/commands.go` — exported version of current `commands.go`:

```go
package client

import "strings"

type SlashCommand struct {
	Name string
	Arg  string
}

func commandArgCount(name string) int {
	switch name {
	case "scan", "discontinue", "inspect", "search", "memory", "forget":
		return 1
	default:
		return 0
	}
}

// ParseCommand checks if text is a slash command. Returns nil if not.
func ParseCommand(text string) *SlashCommand {
	if !strings.HasPrefix(text, "/") {
		return nil
	}

	parts := strings.SplitN(text[1:], " ", 2)
	name := strings.ToLower(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	switch name {
	case "scan", "discontinue", "inspect", "search", "memory", "forget",
		"model", "new", "clear", "commands", "questions":
		return &SlashCommand{Name: name, Arg: arg}
	case "q":
		return &SlashCommand{Name: "questions", Arg: arg}
	default:
		return nil
	}
}

// HelpText returns the /commands help text (handled client-side, no daemon round-trip).
const HelpText = `Projects
  /scan <path>        Scan & memorize a repo
  /discontinue        Remove a project
  /inspect            View worker output

Workers
  /questions          View & answer pending questions
  Ctrl+W              Cycle worker inspection

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
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/client/ -run TestParseCommand -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/client/commands.go internal/client/commands_test.go
git commit -m "feat: implement client-side command parsing"
```

---

## Task 14: TUI Client Model + View

The largest task: the slimmed Bubble Tea model that mirrors daemon state and renders the UI.

**Files:**
- Create: `internal/client/model.go`
- Create: `internal/client/view.go`

- [ ] **Step 1: Implement client model**

`internal/client/model.go` — the Bubble Tea model. Key differences from current `model.go`:
- No `orchestrator`, `halConversation`, `halVoice` — those are daemon-side
- State is mirrored from daemon via `protocol.StateSync` + incremental updates
- Input is translated to protocol messages via `Connection`
- `Update` handles `ServerMsgReceived` instead of direct `halResponseMsg`, `workerStatusMsg`, etc.

The model struct:

```go
package client

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/justin06lee/HAL-9000/internal/config"
	"github.com/justin06lee/HAL-9000/internal/protocol"
	"github.com/justin06lee/HAL-9000/internal/renderer"
)

type tickMsg time.Time

type Model struct {
	width, height int
	startTime     time.Time
	input         textarea.Model
	conn          *Connection

	// Mirrored daemon state
	messages        []protocol.ChatMessage
	workers         map[string]*protocol.WorkerInfo
	specs           []protocol.SpecInfo
	halThinking     bool
	buildStarted    bool
	currentQuestion *protocol.QuestionInfo
	pendingQuestions []protocol.QuestionInfo
	currentModel    string
	currentEffort   string

	// Client-only rendering state
	thinkingFrame   int
	lastHALSpoke    time.Time
	inspecting      string
	chatScroll      int
	quitPending     time.Time
	escPending      time.Time
	pastedContent   string
	pasteLines      int
	lastSentText    string

	// Picker state
	pickerActive         bool
	pickerTitle          string
	pickerOptions        []string
	pickerCursor         int
	pickerAction         string
	modelPickerSection   int
	effortCursor         int
	modelPickerSelected  int
	effortSelected       int

	// Pending confirm (from daemon)
	pendingConfirm string
}

func NewModel(conn *Connection, state *protocol.StateSync) Model {
	ti := textarea.New()
	ti.Placeholder = "Talk to HAL... (Enter send, Shift+Enter newline, Ctrl+B build)"
	ti.Focus()
	ti.CharLimit = 2000
	ti.ShowLineNumbers = false
	ti.SetHeight(3)
	ti.KeyMap.InsertNewline.SetKeys("shift+enter")

	m := Model{
		startTime:    time.Now(),
		input:        ti,
		conn:         conn,
		workers:      make(map[string]*protocol.WorkerInfo),
		lastHALSpoke: time.Now(),
	}

	// Apply initial state sync
	m.applyStateSync(state)
	return m
}

func (m *Model) applyStateSync(state *protocol.StateSync) {
	m.messages = state.Messages
	m.workers = state.Workers
	if m.workers == nil {
		m.workers = make(map[string]*protocol.WorkerInfo)
	}
	m.specs = state.Specs
	m.halThinking = state.HALThinking
	m.buildStarted = state.BuildStarted
	m.currentModel = state.Model
	m.currentEffort = state.Effort
	m.currentQuestion = state.PendingQuestions.Current
	m.pendingQuestions = state.PendingQuestions.Queued
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		textarea.Blink,
		m.conn.ReadMessages(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second/time.Duration(config.EyeFPS), func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		return m, tea.Batch(cmds...)

	case tickMsg:
		if m.halThinking {
			m.thinkingFrame++
		}
		cmds = append(cmds, tickCmd())

	case ServerMsgReceived:
		cmds = append(cmds, m.handleServerMsg(msg)...)
		cmds = append(cmds, m.conn.ReadMessages())

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

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg, cmds)
	}

	// Pre-resize textarea
	if kp, ok := msg.(tea.KeyPressMsg); ok && kp.String() == "shift+enter" {
		newH := max(3, min(m.input.LineCount()+1, m.height/2))
		if newH != m.input.Height() {
			m.input.SetHeight(newH)
		}
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	lineCount := m.input.LineCount()
	newH := max(3, min(lineCount, m.height/2))
	if newH != m.input.Height() {
		m.input.SetHeight(newH)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handleServerMsg(msg ServerMsgReceived) []tea.Cmd {
	switch msg.Type {
	case protocol.MsgStateSync:
		var state protocol.StateSync
		json.Unmarshal(msg.Raw, &state)
		m.applyStateSync(&state)

	case protocol.MsgHALResponse:
		var resp protocol.HALResponse
		json.Unmarshal(msg.Raw, &resp)
		m.halThinking = false
		m.lastHALSpoke = time.Now()
		m.addMsg("hal", "", resp.Text, nil)

	case protocol.MsgHALThinking:
		var t protocol.HALThinking
		json.Unmarshal(msg.Raw, &t)
		m.halThinking = t.Thinking
		if t.Thinking {
			m.thinkingFrame = 0
		}

	case protocol.MsgWorkerStatus:
		var ws protocol.WorkerStatusUpdate
		json.Unmarshal(msg.Raw, &ws)
		if m.workers[ws.Name] == nil {
			m.workers[ws.Name] = &protocol.WorkerInfo{}
		}
		m.workers[ws.Name].Status = ws.Status
		if ws.Activity != "" {
			m.workers[ws.Name].Activity = ws.Activity
		}

	case protocol.MsgWorkerOutput:
		// Only received when inspecting — the view reads from messages
		var wo protocol.WorkerOutputData
		json.Unmarshal(msg.Raw, &wo)
		// Store for inspection view (append to a client-side buffer if needed)

	case protocol.MsgWorkerQuestion:
		var q protocol.QuestionInfo
		json.Unmarshal(msg.Raw, &q)
		m.currentQuestion = &q
		m.addMsg("question", q.Name, q.Text, q.Options)

	case protocol.MsgQuestionResolved:
		m.currentQuestion = nil

	case protocol.MsgPendingQuestions:
		var pq protocol.PendingQuestionsData
		json.Unmarshal(msg.Raw, &pq)
		m.currentQuestion = pq.Current
		m.pendingQuestions = pq.Queued
		// Display
		if pq.Total == 0 {
			m.addMsg("system", "", "No pending questions.", nil)
		} else {
			if pq.Current != nil {
				m.addMsg("question", pq.Current.Name, pq.Current.Text, pq.Current.Options)
			}
			for i, q := range pq.Queued {
				m.addMsg("system", "", fmt.Sprintf("Queued #%d from %s: %s", i+1, q.Name, q.Text), nil)
			}
			m.addMsg("system", "", fmt.Sprintf("%d question(s) pending.", pq.Total), nil)
		}

	case protocol.MsgSystemMessage:
		var text string
		json.Unmarshal(msg.Raw, &text)
		m.addMsg("system", "", text, nil)

	case protocol.MsgModelPicker:
		var mp protocol.ModelPickerData
		json.Unmarshal(msg.Raw, &mp)
		m.pickerActive = true
		m.pickerAction = "model"
		m.modelPickerSection = 0
		m.modelPickerSelected = -1
		m.effortSelected = -1
		// Find current cursor positions
		for i, entry := range mp.Models {
			if entry.ID == mp.CurrentModel {
				m.pickerCursor = i
			}
		}
		for i, e := range mp.Efforts {
			if e == mp.CurrentEffort {
				m.effortCursor = i
			}
		}

	case protocol.MsgConfirmPrompt:
		var cp protocol.ConfirmPromptData
		json.Unmarshal(msg.Raw, &cp)
		m.pendingConfirm = cp.Action
		m.addMsg("system", "", cp.Text, nil)

	case protocol.MsgError:
		var text string
		json.Unmarshal(msg.Raw, &text)
		m.addMsg("system", "", "Error: "+text, nil)

	case "disconnected":
		m.addMsg("system", "", "Disconnected from daemon.", nil)
	}

	return nil
}

func (m Model) handleKeyPress(msg tea.KeyPressMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	// Reset placeholder
	defaultPlaceholder := "Talk to HAL... (Enter send, Shift+Enter newline, Ctrl+B build)"
	if m.input.Placeholder != defaultPlaceholder {
		if msg.String() != "ctrl+c" && msg.String() != "ctrl+q" && msg.String() != "esc" {
			m.input.Placeholder = defaultPlaceholder
		}
	}

	// Picker mode
	if m.pickerActive {
		return m.handlePickerKey(msg, cmds)
	}

	switch msg.String() {
	case "ctrl+c", "ctrl+q":
		if m.halThinking {
			m.conn.SendInterrupt()
			m.halThinking = false
			if m.lastSentText != "" {
				m.input.SetValue(m.lastSentText)
				m.lastSentText = ""
			}
			m.addMsg("system", "", "Interrupted.", nil)
			return m, tea.Batch(cmds...)
		}
		if !m.quitPending.IsZero() && time.Since(m.quitPending) < 2*time.Second {
			m.conn.Close()
			return m, tea.Quit
		}
		m.quitPending = time.Now()
		m.input.Placeholder = "Press Ctrl+C again to quit (daemon keeps running)"
		return m, tea.Batch(cmds...)

	case "ctrl+b":
		m.conn.SendStartBuild()

	case "ctrl+y":
		if text := lastHALMessage(m.messages); text != "" {
			copyToClipboard(text)
			m.addMsg("system", "", "Copied last HAL response to clipboard.", nil)
		}

	case "ctrl+w":
		names := m.workerNames()
		sort.Strings(names)
		if len(names) == 0 {
			m.addMsg("system", "", "No workers to inspect.", nil)
		} else if m.inspecting == "" {
			m.inspecting = names[0]
			m.conn.SendCommand("inspect", names[0])
			m.chatScroll = 0
		} else {
			next := ""
			for i, n := range names {
				if n == m.inspecting && i+1 < len(names) {
					next = names[i+1]
					break
				}
			}
			m.conn.SendCommand("inspect_stop", "")
			if next == "" {
				m.inspecting = ""
				m.chatScroll = 0
			} else {
				m.inspecting = next
				m.conn.SendCommand("inspect", next)
				m.chatScroll = 0
			}
		}

	case "pgup", "shift+up":
		m.chatScroll += 10
	case "pgdown", "shift+down":
		m.chatScroll = max(0, m.chatScroll-10)
	case "home":
		m.chatScroll = 999999
	case "end":
		m.chatScroll = 0

	case "esc":
		if m.halThinking {
			m.conn.SendInterrupt()
			m.halThinking = false
			m.addMsg("system", "", "Interrupted.", nil)
			return m, tea.Batch(cmds...)
		}
		if m.inspecting != "" {
			m.conn.SendCommand("inspect_stop", "")
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
		if m.pastedContent != "" {
			summary := fmt.Sprintf("[pasted %d lines]", m.pasteLines)
			text = strings.Replace(text, summary, m.pastedContent, 1)
			m.pastedContent = ""
			m.pasteLines = 0
		}
		if text != "" {
			m.lastSentText = text
			m.input.SetValue("")

			// Handle pending confirm
			if m.pendingConfirm != "" {
				m.pendingConfirm = ""
				m.conn.SendText(text)
				return m, tea.Batch(cmds...)
			}

			// Parse slash commands
			if cmd := ParseCommand(text); cmd != nil {
				if cmd.Name == "commands" {
					m.addMsg("system", "", HelpText, nil)
				} else {
					m.conn.SendCommand(cmd.Name, cmd.Arg)
				}
				return m, tea.Batch(cmds...)
			}

			// Answer question or send message
			if m.currentQuestion != nil {
				answer := text
				if idx, err := strconv.Atoi(text); err == nil && idx >= 1 && idx <= len(m.currentQuestion.Options) {
					answer = m.currentQuestion.Options[idx-1]
				}
				m.conn.SendAnswer(m.currentQuestion.Name, answer)
			} else {
				m.addMsg("user", "", text, nil)
				m.conn.SendText(text)
			}
		}
		return m, tea.Batch(cmds...)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handlePickerKey(msg tea.KeyPressMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if m.pickerAction == "model" {
		// Model picker: same logic as current model.go
		switch msg.String() {
		case "up", "k":
			if m.modelPickerSection == 1 {
				if m.effortCursor > 0 {
					m.effortCursor--
				} else {
					m.modelPickerSection = 0
				}
			} else if m.pickerCursor > 0 {
				m.pickerCursor--
			}
		case "down", "j":
			if m.modelPickerSection == 0 {
				if m.pickerCursor < 2 { // 3 models
					m.pickerCursor++
				} else {
					m.modelPickerSection = 1
					m.effortCursor = 0
				}
			} else if m.effortCursor < 3 { // 4 effort levels
				m.effortCursor++
			}
		case "enter":
			if m.modelPickerSection == 0 {
				m.modelPickerSelected = m.pickerCursor
				m.modelPickerSection = 1
			} else {
				m.effortSelected = m.effortCursor
				m.pickerActive = false
				// Send model + effort to daemon as "model_id:effort"
				idx := m.pickerCursor
				if m.modelPickerSelected >= 0 {
					idx = m.modelPickerSelected
				}
				eIdx := m.effortCursor
				if m.effortSelected >= 0 {
					eIdx = m.effortSelected
				}
				m.conn.SendCommand("model", config.ModelIDs[idx]+":"+config.EffortLevels[eIdx])
				m.currentModel = config.ModelIDs[idx]
				m.currentEffort = config.EffortLevels[eIdx]
			}
		case "esc":
			m.pickerActive = false
		}
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) addMsg(role, name, text string, options []string) {
	m.messages = append(m.messages, protocol.ChatMessage{
		Role: role, Name: name, Text: text, Options: options,
	})
	if len(m.messages) > config.MaxMessages {
		m.messages = m.messages[len(m.messages)-config.MaxMessages:]
	}
	if m.chatScroll <= 3 {
		m.chatScroll = 0
	}
}

func (m *Model) workerNames() []string {
	names := make([]string, 0, len(m.workers))
	for n := range m.workers {
		names = append(names, n)
	}
	return names
}
```

- [ ] **Step 2: Implement client view**

`internal/client/view.go` — adapted from current `view.go`. The key change is reading worker info from `map[string]*protocol.WorkerInfo` instead of `map[string]workerStatus` + orchestrator. All ANSI helpers, `formatWorkers`, `formatChat`, `formatInspection` are moved here with minor type adaptations.

This file is largely a copy of the current `view.go` with:
- `package client`
- Worker status reads from `m.workers[name].Status` and `m.workers[name].Activity`
- No `orchestrator` reference — all data comes from the mirrored protocol types
- `renderEye` calls become `renderer.RenderEye`

- [ ] **Step 3: Helper functions**

Move `lastHALMessage`, `copyToClipboard`, `firstSentence` into `internal/client/model.go` as unexported helpers.

- [ ] **Step 4: Verify it compiles**

```bash
go build ./internal/client/
```

Expected: compiles.

- [ ] **Step 5: Commit**

```bash
git add internal/client/model.go internal/client/view.go
git commit -m "feat: implement TUI client model and view with daemon state mirroring"
```

---

## Task 15: TUI Client Entry Point

**Files:**
- Modify: `cmd/hal9000/main.go` (replace stub)

- [ ] **Step 1: Implement client main**

`cmd/hal9000/main.go`:

```go
package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/justin06lee/HAL-9000/internal/client"
	"github.com/justin06lee/HAL-9000/internal/config"
)

const version = "0.3.0"

func main() {
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--version", "-v", "version":
			fmt.Printf("hal9000 v%s\n", version)
			return
		case "--help", "-h", "help":
			fmt.Println("HAL 9000 — AI Project Orchestrator (TUI Client)")
			fmt.Printf("Version: %s\n\n", version)
			fmt.Println("Usage: hal9000 [options]")
			fmt.Println("")
			fmt.Println("Connects to the HAL 9000 daemon (auto-starts if not running).")
			fmt.Println("On quit (Ctrl+Q), the daemon keeps running in the background.")
			fmt.Println("")
			fmt.Println("Options:")
			fmt.Println("  --model <model>       Set Claude model on connect")
			fmt.Println("  --thinking <level>    Set effort level on connect")
			fmt.Println("  --version             Show version")
			return
		case "--model":
			if i+1 < len(args) {
				i++
				config.ClaudeModel = args[i]
			}
		case "--thinking":
			if i+1 < len(args) {
				i++
				config.ThinkingBudget = args[i]
			}
		}
	}

	// Connect to daemon
	conn, err := client.Connect(config.SocketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Is the daemon running? Try: hal9000d")
		os.Exit(1)
	}

	state, err := conn.Subscribe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		conn.Close()
		os.Exit(1)
	}

	m := client.NewModel(conn, state)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Build client binary**

```bash
go build -o hal9000 ./cmd/hal9000/
```

Expected: compiles.

- [ ] **Step 3: Commit**

```bash
git add cmd/hal9000/main.go
git commit -m "feat: implement TUI client entry point"
```

---

## Task 16: Integration Build + Cleanup

Build both binaries, verify they compile, remove old root-level files.

**Files:**
- Delete: all root-level `.go` files (`config.go`, `hal.go`, `orchestrator.go`, `model.go`, `view.go`, `commands.go`, `memory.go`, `rag.go`, `scanner.go`, `voice.go`, `session.go`, `renderer.go`, `main.go`)
- Modify: `go.mod` if needed

- [ ] **Step 1: Build both binaries**

```bash
go build -o hal9000d ./cmd/hal9000d/
go build -o hal9000 ./cmd/hal9000/
```

Expected: both compile.

- [ ] **Step 2: Run all tests**

```bash
go test ./internal/... -v
```

Expected: all tests pass.

- [ ] **Step 3: Remove old root-level Go files**

```bash
rm config.go hal.go orchestrator.go model.go view.go commands.go memory.go rag.go scanner.go voice.go session.go renderer.go main.go
```

- [ ] **Step 4: Verify clean build from scratch**

```bash
go build ./...
go test ./...
```

Expected: compiles and tests pass with only the new package structure.

- [ ] **Step 5: Update .gitignore**

Add both binary names if not already present:
```
hal9000
hal9000d
```

- [ ] **Step 6: Copy menu bar icon to assets**

```bash
cp ~/Pictures/pfp/hal9000taskbar.png assets/hal9000taskbar.png
```

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: complete daemon/client split — remove monolithic files, build both binaries"
```

---

## Task 17: Manual Integration Test

Not automated — run the daemon and client together to verify end-to-end.

- [ ] **Step 1: Start daemon in foreground**

```bash
./hal9000d
```

Expected: daemon starts, menu bar icon appears, socket created at `~/.config/hal9000/hal9000.sock`.

- [ ] **Step 2: Connect client in another terminal**

```bash
./hal9000
```

Expected: TUI appears with HAL eye, greeting message, worker panel. State synced from daemon.

- [ ] **Step 3: Test basic interaction**

- Type a message → appears in chat, HAL responds
- `/commands` → shows help text (client-side)
- `/model` → shows model picker (daemon sends `model_picker`)
- Ctrl+Q → client disconnects, daemon keeps running

- [ ] **Step 4: Test reconnection**

```bash
./hal9000
```

Expected: reconnects to running daemon, receives `state_sync` with previous messages and state.

- [ ] **Step 5: Test install/uninstall**

```bash
./hal9000d install
launchctl list | grep hal9000
./hal9000d uninstall
```

Expected: plist created/loaded, daemon starts on boot, uninstall removes it.

- [ ] **Step 6: Commit any fixes found during testing**

```bash
git add -A
git commit -m "fix: integration test fixes"
```
