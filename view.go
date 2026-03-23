package main

import (
	"fmt"
	"strings"
	"time"
)

// ── ANSI helpers ────────────────────────────────────────────

func visibleLen(s string) int {
	n := 0
	inEscape := false
	for _, c := range s {
		if c == '\033' {
			inEscape = true
		} else if inEscape {
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
				inEscape = false
			}
		} else {
			n++
		}
	}
	return n
}

func padRight(s string, width int) string {
	vl := visibleLen(s)
	if vl >= width {
		return s
	}
	return s + strings.Repeat(" ", width-vl)
}

func truncateVisible(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	runes := []rune(s)
	n := 0
	inEscape := false
	for i, c := range runes {
		if c == '\033' {
			inEscape = true
		} else if inEscape {
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
				inEscape = false
			}
		} else {
			n++
			if n > maxWidth {
				return string(runes[:i]) + "\033[0m"
			}
		}
	}
	return s
}

// ── View ────────────────────────────────────────────────────

func (m model) View() string {
	if m.width < 30 || m.height < 15 {
		return "Terminal too small. Need at least 30x15."
	}

	available := m.height - 2 // header + footer
	eyeH := max(5, available*40/100)
	bottomH := max(5, available-eyeH)
	workerW := 24
	chatW := m.width - workerW - 1 // -1 for separator

	var sb strings.Builder
	sb.Grow(m.width * m.height * 20)

	// ── Header
	title := " HAL 9000 "
	pad := max(0, (m.width-len(title))/2)
	sb.WriteString("\033[48;2;26;5;5m\033[38;2;204;51;34;1m")
	sb.WriteString(strings.Repeat(" ", pad))
	sb.WriteString(title)
	sb.WriteString(strings.Repeat(" ", max(0, m.width-pad-len(title))))
	sb.WriteString("\033[0m\n")

	// ── Eye
	elapsed := time.Since(m.startTime).Seconds()
	sinceSpeech := time.Since(m.lastHALSpoke).Seconds()
	sb.WriteString(renderEye(m.width, eyeH, elapsed, eyeCycleSeconds, sinceSpeech))
	sb.WriteByte('\n')

	// ── Bottom: Workers | Chat
	workerLines := formatWorkers(m.workerStatuses, workerW, bottomH)
	var chatLines []string
	if m.inspecting != "" {
		chatLines = formatInspection(m.inspecting, m.orch.getWorkerOutput(m.inspecting), m.input.View(), chatW, bottomH)
	} else {
		chatLines = formatChat(m.messages, m.input.View(), chatW, bottomH)
	}

	for i := 0; i < bottomH; i++ {
		wl := ""
		if i < len(workerLines) {
			wl = workerLines[i]
		}
		cl := ""
		if i < len(chatLines) {
			cl = chatLines[i]
		}
		sb.WriteString(padRight(wl, workerW))
		sb.WriteString("\033[38;2;60;60;60m\u2502\033[0m")
		sb.WriteString(cl)
		if i < bottomH-1 {
			sb.WriteByte('\n')
		}
	}

	// ── Footer
	sb.WriteByte('\n')
	sb.WriteString("\033[48;2;15;15;26m\033[38;2;100;100;100m")
	footer := " Ctrl+B build \u2502 Ctrl+W inspect \u2502 Ctrl+Y copy \u2502 /scan <path> \u2502 Ctrl+Q quit"
	sb.WriteString(footer)
	sb.WriteString(strings.Repeat(" ", max(0, m.width-visibleLen(footer))))
	sb.WriteString("\033[0m")

	return sb.String()
}

// ── Workers panel ───────────────────────────────────────────

func formatWorkers(statuses map[string]workerStatus, w, h int) []string {
	lines := make([]string, 0, h)

	lines = append(lines, "\033[38;2;220;50;20;1m WORKERS\033[0m")
	lines = append(lines, "\033[38;2;60;60;60m "+strings.Repeat("\u2500", w-2)+"\033[0m")

	if len(statuses) == 0 {
		lines = append(lines, "\033[38;2;80;80;80;3m No workers yet\033[0m")
	} else {
		for name, status := range statuses {
			var icon, color string
			switch status {
			case statusPending:
				icon, color = "\u2500", "80;80;80"
			case statusRunning:
				icon, color = "\u25b6", "80;200;120"
			case statusWaiting:
				icon, color = "?", "255;200;50"
			case statusCompleted:
				icon, color = "\u2713", "80;220;80"
			case statusAuditing:
				icon, color = "\u25cb", "100;150;255"
			case statusDone:
				icon, color = "\u2714", "50;255;100"
			case statusFailed:
				icon, color = "\u2717", "220;50;50"
			case statusAuditFailed:
				icon, color = "!", "255;150;50"
			default:
				icon, color = "?", "100;100;100"
			}
			lines = append(lines,
				fmt.Sprintf(" \033[38;2;%s;1m%s\033[0m \033[38;2;180;180;180m%s\033[0m", color, icon, truncateVisible(name, w-5)))
			lines = append(lines,
				fmt.Sprintf("   \033[38;2;%sm%s\033[0m", color, status.String()))
		}
	}

	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

// ── Chat area ───────────────────────────────────────────────

func formatChat(messages []chatMessage, inputView string, w, h int) []string {
	// Split textarea view into lines to calculate how much space it needs
	inputLines := strings.Split(inputView, "\n")
	inputH := max(1, len(inputLines))

	msgH := max(1, h-inputH)

	// Format messages into lines
	var allMsgLines []string
	for _, msg := range messages {
		allMsgLines = append(allMsgLines, formatMessage(msg, w)...)
	}

	lines := make([]string, 0, h)

	// Show last msgH lines of messages
	start := max(0, len(allMsgLines)-msgH)
	for i := start; i < len(allMsgLines); i++ {
		lines = append(lines, allMsgLines[i])
	}

	// Pad to msgH
	for len(lines) < msgH {
		lines = append(lines, "")
	}

	// Input area at bottom (multi-line)
	for _, il := range inputLines {
		lines = append(lines, " "+truncateVisible(il, w-2))
	}

	return lines[:h]
}

func formatMessage(msg chatMessage, maxW int) []string {
	switch msg.role {
	case "hal":
		return formatPrefixed("\033[38;2;220;50;20;1m HAL \033[0m", "\033[38;2;200;200;200m", msg.text, maxW)
	case "user":
		return formatPrefixed("\033[38;2;80;180;255;1m YOU \033[0m", "\033[38;2;230;230;230m", msg.text, maxW)
	case "system":
		return []string{"\033[38;2;100;100;100;3m " + truncateVisible(msg.text, maxW-2) + "\033[0m"}
	case "worker":
		prefix := fmt.Sprintf("\033[38;2;180;140;60;1m [%s] \033[0m", msg.name)
		return formatPrefixed(prefix, "\033[38;2;160;160;160m", msg.text, maxW)
	case "question":
		var lines []string
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("\033[38;2;255;200;50;1m QUESTION from %s:\033[0m", msg.name))
		lines = append(lines, "  \033[38;2;230;230;230m"+truncateVisible(msg.text, maxW-4)+"\033[0m")
		for i, opt := range msg.options {
			lines = append(lines, fmt.Sprintf("  \033[38;2;180;220;255m[%d] %s\033[0m", i+1, opt))
		}
		lines = append(lines, "  \033[38;2;120;120;120mType a number or answer:\033[0m")
		return lines
	}
	return []string{" " + msg.text}
}

func formatPrefixed(prefix, style, text string, maxW int) []string {
	wrapped := wrapText(text, max(20, maxW-7))
	var lines []string
	for i, line := range wrapped {
		if i == 0 {
			lines = append(lines, prefix+style+line+"\033[0m")
		} else {
			lines = append(lines, "      "+style+line+"\033[0m")
		}
	}
	return lines
}

func wrapText(text string, width int) []string {
	if width <= 0 {
		width = 40
	}

	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		current := words[0]
		for _, word := range words[1:] {
			if len(current)+1+len(word) > width {
				lines = append(lines, current)
				current = word
			} else {
				current += " " + word
			}
		}
		lines = append(lines, current)
	}

	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// ── Inspection view ─────────────────────────────────────────

func formatInspection(workerName string, outputLog []string, inputView string, w, h int) []string {
	inputLines := strings.Split(inputView, "\n")
	inputH := max(1, len(inputLines))

	headerH := 2
	logH := max(1, h-inputH-headerH)

	lines := make([]string, 0, h)

	// Header
	lines = append(lines, fmt.Sprintf("\033[38;2;100;150;255;1m [Inspecting: %s]\033[0m", truncateVisible(workerName, w-16)))
	lines = append(lines, "\033[38;2;80;80;80m Ctrl+W cycle \u2502 Esc exit\033[0m")

	// Log output — show last logH lines
	if len(outputLog) == 0 {
		lines = append(lines, "\033[38;2;80;80;80;3m No output yet\033[0m")
		logH--
	} else {
		start := max(0, len(outputLog)-logH)
		for i := start; i < len(outputLog); i++ {
			text := strings.TrimSpace(outputLog[i])
			if len(text) > w-2 {
				text = text[:w-2]
			}
			lines = append(lines, " \033[38;2;160;160;160m"+text+"\033[0m")
		}
		shown := min(logH, len(outputLog))
		logH -= shown
	}

	// Pad remaining log area
	for i := 0; i < logH; i++ {
		lines = append(lines, "")
	}

	// Input area
	for _, il := range inputLines {
		lines = append(lines, " "+truncateVisible(il, w-2))
	}

	return lines[:h]
}
