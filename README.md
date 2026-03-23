<p align="center">
  <img src="hal9000.png" width="200" />
</p>

# HAL 9000

An AI project orchestrator powered by Claude Code. HAL manages multiple software projects simultaneously — scanning repos, delegating work to AI workers, remembering context across sessions, and speaking to you with a synthesized voice.

> **No API keys. No SDK. Just [Claude Code](https://docs.anthropic.com/en/docs/claude-code/overview).**
>
> HAL runs entirely on top of the Claude Code CLI — if you have `claude` installed, you're good to go.

## Requirements

- **Go 1.21+** — [install](https://go.dev/dl/)
- **Claude Code CLI** — `npm install -g @anthropic-ai/claude-code`
- **macOS** (voice synthesis uses `say` as fallback; Piper TTS is auto-downloaded)

## Install

```bash
git clone https://github.com/justin06lee/HAL-9000.git
cd HAL-9000
go build -o hal9000 .
./hal9000
```

Or use the launcher script which auto-rebuilds when source files change:

```bash
./run.sh
```

### Options

```
hal9000 [flags]

  --model <model>       Claude model (sonnet, opus, haiku)
  --thinking <budget>   Thinking budget (e.g. 10000, high, low)
  --version             Show version
```

## Usage

### Commands

| Command | Description |
|---|---|
| `/scan <path>` | Scan a repo for HAL to analyze and memorize |
| `/discontinue` | Remove a project |
| `/inspect` | View a worker's output |
| `/search <query>` | Search project memory using RAG |
| `/memory` | Browse stored memory by project and topic |
| `/forget <project>` | Delete a project's memory |
| `/forget all` | Delete all memory |
| `/model` | Change Claude model and thinking effort |
| `/new` | Start a fresh session |
| `/commands` | Show help |
| `/clear` | Clear chat (keeps last message) |

### Keybindings

| Key | Action |
|---|---|
| `Enter` | Send message |
| `Shift+Enter` | New line |
| `Ctrl+B` | Start build |
| `Ctrl+Y` | Copy last HAL response |
| `Ctrl+C` | Interrupt HAL / quit |
| `Esc` | Interrupt HAL / clear input |
| `PgUp / PgDn` | Scroll chat |

### Voice

HAL speaks responses aloud using [Piper TTS](https://github.com/rhasspy/piper) (auto-downloaded on first run). Falls back to macOS `say` if Piper isn't available.

### Memory

HAL automatically remembers project context across sessions. Memory is organized per-project with categorized topics (overview, architecture, requirements, tech stack, decisions, notes). A local TF-IDF search index lets HAL retrieve relevant context on demand instead of loading everything at once.

## License

MIT
