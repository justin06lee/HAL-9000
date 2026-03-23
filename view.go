package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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

// colorCommandWord finds the slash command word in a rendered textarea line
// (which contains ANSI escape sequences) and wraps just that word with orange.
// If colorRest is true, everything after the command word is also colored.
func colorCommandWord(rendered, cmdWord string, colorRest bool) string {
	runes := []rune(rendered)
	cmdRunes := []rune(cmdWord)
	var out []rune
	matched := 0
	matchStart := -1
	inEscape := false

	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == '\033' {
			inEscape = true
			out = append(out, c)
			continue
		}
		if inEscape {
			out = append(out, c)
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
				inEscape = false
			}
			continue
		}
		// Visible character
		if matched < len(cmdRunes) && c == cmdRunes[matched] {
			if matched == 0 {
				matchStart = len(out)
			}
			matched++
			out = append(out, c)
			if matched == len(cmdRunes) {
				// Found the command word — inject orange
				colored := make([]rune, 0, len(out)+40)
				colored = append(colored, out[:matchStart]...)
				colored = append(colored, []rune("\033[38;2;255;180;80m")...)
				colored = append(colored, out[matchStart:]...)
				if colorRest {
					// Color the rest of the line too (args)
					colored = append(colored, runes[i+1:]...)
					colored = append(colored, []rune("\033[0m")...)
					return string(colored)
				}
				colored = append(colored, []rune("\033[0m")...)
				colored = append(colored, runes[i+1:]...)
				return string(colored)
			}
		} else {
			matched = 0
			matchStart = -1
			out = append(out, c)
		}
	}
	return string(out)
}

// ── View ────────────────────────────────────────────────────

func (m model) View() tea.View {
	if m.width < 30 || m.height < 15 {
		v := tea.NewView("Terminal too small. Need at least 30x15.")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	available := m.height - 1 // header only (no footer)
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
		chatLines = formatInspection(m.inspecting, m.orch.getWorkerOutput(m.inspecting), m.input.View(), chatW, bottomH, m.chatScroll)
	} else {
		thinkFrame := -1
		if m.halThinking {
			thinkFrame = m.thinkingFrame
		}
		chatLines = formatChat(m.messages, m.input.View(), chatW, bottomH, m.chatScroll, thinkFrame, &m)
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


	v := tea.NewView(sb.String())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
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

func formatChat(messages []chatMessage, inputView string, w, h, scroll, thinkFrame int, m *model) []string {
	inputLines := strings.Split(inputView, "\n")
	inputH := max(1, len(inputLines))

	// Message area = total height minus input
	msgH := max(1, h-inputH)

	// Scrollbar takes 1 char from message width
	contentW := w - 1 // leave 1 col for scrollbar

	// Format messages into lines
	var allMsgLines []string
	for _, msg := range messages {
		allMsgLines = append(allMsgLines, formatMessage(msg, contentW)...)
	}

	// Append animated thinking indicator
	if thinkFrame >= 0 {
		allMsgLines = append(allMsgLines, renderThinkingShimmer(thinkFrame, contentW, "thinking"))
	}

	// Append picker if active
	if m.pickerActive {
		allMsgLines = append(allMsgLines, "")
		if m.pickerAction == "model" {
			// Two-section model picker
			allMsgLines = append(allMsgLines, "\033[38;2;220;140;60m Select Model\033[0m")
			for i, entry := range claudeModelList {
				hovering := m.modelPickerSection == 0 && i == m.pickerCursor
				locked := m.modelPickerSelected == i
				prefix := "   "
				color := "120;120;120"
				if hovering {
					prefix = " › "
					color = "255;220;180"
				} else if locked {
					prefix = " ✓ "
					color = "255;255;255"
				}
				allMsgLines = append(allMsgLines, fmt.Sprintf(" \033[38;2;%sm%s%s\033[0m", color, prefix, entry.display))
			}
			allMsgLines = append(allMsgLines, "")
			allMsgLines = append(allMsgLines, "\033[38;2;220;140;60m Select Effort\033[0m")
			for i, e := range effortLevels {
				hovering := m.modelPickerSection == 1 && i == m.effortCursor
				locked := m.effortSelected == i
				prefix := "   "
				color := "120;120;120"
				if hovering {
					prefix = " › "
					color = "255;220;180"
				} else if locked {
					prefix = " ✓ "
					color = "255;255;255"
				}
				allMsgLines = append(allMsgLines, fmt.Sprintf(" \033[38;2;%sm%s%s\033[0m", color, prefix, e))
			}
			allMsgLines = append(allMsgLines, "")
			allMsgLines = append(allMsgLines, "\033[38;2;80;80;80m ↑↓ navigate · Enter select · Esc confirm & exit\033[0m")
		} else {
			// Standard picker
			allMsgLines = append(allMsgLines, fmt.Sprintf("\033[38;2;220;140;60m %s\033[0m", m.pickerTitle))
			for i, opt := range m.pickerOptions {
				if i == m.pickerCursor {
					allMsgLines = append(allMsgLines, fmt.Sprintf(" \033[38;2;255;220;180;1m › %s\033[0m", truncateVisible(opt, contentW-4)))
				} else {
					allMsgLines = append(allMsgLines, fmt.Sprintf(" \033[38;2;120;120;120m   %s\033[0m", truncateVisible(opt, contentW-4)))
				}
			}
			allMsgLines = append(allMsgLines, "\033[38;2;80;80;80m ↑↓ navigate · Enter select · Esc cancel\033[0m")
		}
	}

	totalLines := len(allMsgLines)

	// Clamp scroll
	maxScroll := max(0, totalLines-msgH)
	if scroll > maxScroll {
		scroll = maxScroll
	}

	// Visible window
	end := max(0, totalLines-scroll)
	start := max(0, end-msgH)
	visibleCount := end - start

	lines := make([]string, 0, h)

	// Render message lines with scrollbar
	for i := 0; i < msgH; i++ {
		line := ""
		if i < visibleCount {
			line = allMsgLines[start+i]
		}

		// Scrollbar character
		scrollChar := renderScrollbarChar(i, msgH, totalLines, start, visibleCount)
		lines = append(lines, padRight(line, contentW)+scrollChar)
	}

	// Input area at bottom (fixed, no scrollbar)
	// Find valid slash commands and their args to highlight
	val := strings.TrimSpace(m.input.Value())
	fields := strings.Fields(val)
	// Collect spans to highlight: each is the text to match + whether to color the rest
	type cmdSpan struct {
		text      string // visible text to match (command + args joined)
		colorRest bool   // color everything after match too
	}
	var spans []cmdSpan
	for i, word := range fields {
		if !strings.HasPrefix(word, "/") {
			continue
		}
		cmd := parseCommand(word)
		if cmd == nil {
			continue
		}
		// Build the highlight text: command + up to N arg words
		parts := []string{word}
		argCount := commandArgCount(cmd.name)
		for j := 1; j <= argCount && i+j < len(fields); j++ {
			parts = append(parts, fields[i+j])
		}
		highlight := strings.Join(parts, " ")
		// If command is at the start, also color any remaining args
		isLeading := i == 0
		spans = append(spans, cmdSpan{text: highlight, colorRest: isLeading && argCount > 0})
	}
	for _, il := range inputLines {
		rendered := truncateVisible(il, w-2)
		for _, sp := range spans {
			rendered = colorCommandWord(rendered, sp.text, sp.colorRest)
		}
		lines = append(lines, " "+rendered)
	}

	return lines[:h]
}

// renderScrollbarChar returns the scrollbar character for a given row
func renderScrollbarChar(row, viewH, totalLines, startLine, visibleCount int) string {
	if totalLines <= viewH {
		// Everything fits, no scrollbar needed — dim track
		return "\033[38;2;30;30;30m\u2502\033[0m"
	}

	// Calculate thumb position and size
	thumbSize := max(1, viewH*viewH/totalLines)
	thumbPos := startLine * viewH / totalLines

	if row >= thumbPos && row < thumbPos+thumbSize {
		// Thumb — bright
		return "\033[38;2;140;50;40m\u2588\033[0m"
	}
	// Track — dim
	return "\033[38;2;35;35;35m\u2502\033[0m"
}

// renderThinkingShimmer creates the shiny/glowing text animation
func renderThinkingShimmer(frame, maxW int, label string) string {
	text := fmt.Sprintf("HAL is %s...", label)
	runes := []rune(text)
	n := len(runes)

	// Shimmer moves across the text like a light sweep
	speed := 3        // chars per frame at 18fps → smooth sweep
	shimmerWidth := 6 // width of the bright highlight
	pos := (frame * speed) % (n + shimmerWidth + 10)

	var sb strings.Builder
	sb.WriteString(" ")
	for i, r := range runes {
		dist := pos - i
		if dist < 0 {
			dist = -dist
		}

		var cr, cg, cb int
		if dist <= shimmerWidth/2 {
			// Bright highlight center: white-hot glow
			t := 1.0 - float64(dist)/float64(shimmerWidth/2+1)
			cr = 220 + int(35*t)
			cg = 50 + int(200*t)
			cb = 20 + int(235*t)
		} else if dist <= shimmerWidth {
			// Fade edge: reddish-orange glow
			cr = 220
			cg = 70
			cb = 40
		} else {
			// Base: dim red (HAL color)
			cr = 140
			cg = 40
			cb = 30
		}
		fmt.Fprintf(&sb, "\033[38;2;%d;%d;%dm%c", cr, cg, cb, r)
	}
	sb.WriteString("\033[0m")
	_ = n // used in modular arithmetic above
	return truncateVisible(sb.String(), maxW)
}

func formatMessage(msg chatMessage, maxW int) []string {
	switch msg.role {
	case "hal":
		return formatPrefixed("\033[38;2;220;50;20;1m HAL \033[0m", "\033[38;2;200;200;200m", msg.text, maxW)
	case "user":
		// Color slash commands orange in user messages
		fields := strings.Fields(msg.text)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "/") && parseCommand(fields[0]) != nil {
			return formatPrefixed("\033[38;2;80;180;255;1m YOU \033[0m", "\033[38;2;255;180;80m", msg.text, maxW)
		}
		return formatPrefixed("\033[38;2;80;180;255;1m YOU \033[0m", "\033[38;2;230;230;230m", msg.text, maxW)
	case "system":
		var sysLines []string
		for _, line := range strings.Split(msg.text, "\n") {
			if visibleLen(line) <= maxW-3 {
				sysLines = append(sysLines, "\033[38;2;100;100;100;3m "+line+"\033[0m")
			} else {
				// Only wrap lines that are too long
				wrapped := wrapText(line, max(10, maxW-3))
				for _, wl := range wrapped {
					sysLines = append(sysLines, "\033[38;2;100;100;100;3m "+wl+"\033[0m")
				}
			}
		}
		return sysLines
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
	codeStyle := "\033[38;2;80;220;220m"
	inCodeBlock := false
	wrapped := wrapText(text, max(20, maxW-7))
	var lines []string
	for i, line := range wrapped {
		// Handle fenced code blocks (``` or ```lang)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue // skip the fence line
		}
		var colored string
		if inCodeBlock {
			colored = codeStyle + line
		} else {
			colored = renderMarkdownColors(line, style)
		}
		if i == 0 || (i == 1 && len(lines) == 0) {
			lines = append(lines, prefix+colored+"\033[0m")
		} else {
			lines = append(lines, "      "+colored+"\033[0m")
		}
	}
	return lines
}

var (
	mdBoldRe      = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalicRe    = regexp.MustCompile(`(?:^|[^*])\*([^*]+)\*(?:[^*]|$)`)
	mdCodeRe      = regexp.MustCompile("`([^`]+)`")
	mdHeadingRe   = regexp.MustCompile(`^(#{1,3})\s+(.+)`)
	mdBulletRe    = regexp.MustCompile(`^(\s*[-*])\s+`)
	mdNumberedRe  = regexp.MustCompile(`^(\s*\d+\.)\s+`)
)

// renderMarkdownColors converts markdown syntax to ANSI colors
func renderMarkdownColors(line, baseStyle string) string {
	// Headers → bright white
	if m := mdHeadingRe.FindStringSubmatch(line); m != nil {
		return "\033[38;2;255;220;180m" + m[2]
	}

	// Bullet points → orange bullet + normal text
	if m := mdBulletRe.FindStringSubmatchIndex(line); m != nil {
		rest := line[m[1]:]
		line = "\033[38;2;220;140;60m• " + baseStyle + rest
	} else if m := mdNumberedRe.FindStringSubmatchIndex(line); m != nil {
		num := line[m[2]:m[3]]
		rest := line[m[1]:]
		line = "\033[38;2;220;140;60m" + num + " " + baseStyle + rest
	}

	// Bold **text** → red/orange
	line = mdBoldRe.ReplaceAllString(line, "\033[38;2;255;100;70m$1"+baseStyle)

	// Code `text` → cyan
	line = mdCodeRe.ReplaceAllString(line, "\033[38;2;80;220;220m$1"+baseStyle)

	// Italic *text* → yellow (must be after bold to avoid conflicts)
	// Only match single * not preceded/followed by *
	line = renderItalic(line, baseStyle)

	return baseStyle + line
}

func renderItalic(line, baseStyle string) string {
	var result strings.Builder
	runes := []rune(line)
	n := len(runes)
	i := 0
	for i < n {
		if runes[i] == '*' && (i+1 < n && runes[i+1] != '*') && (i == 0 || runes[i-1] != '*') {
			// Find closing *
			end := -1
			for j := i + 1; j < n; j++ {
				if runes[j] == '*' && (j+1 >= n || runes[j+1] != '*') {
					end = j
					break
				}
			}
			if end > i+1 {
				result.WriteString("\033[38;2;230;200;100m")
				result.WriteString(string(runes[i+1 : end]))
				result.WriteString(baseStyle)
				i = end + 1
				continue
			}
		}
		result.WriteRune(runes[i])
		i++
	}
	return result.String()
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

func formatInspection(workerName string, outputLog []string, inputView string, w, h, scroll int) []string {
	inputLines := strings.Split(inputView, "\n")
	inputH := max(1, len(inputLines))

	headerH := 2
	indicatorH := 0
	if scroll > 0 {
		indicatorH = 1
	}
	logH := max(1, h-inputH-headerH-indicatorH)

	lines := make([]string, 0, h)

	// Header
	lines = append(lines, fmt.Sprintf("\033[38;2;100;150;255;1m [Inspecting: %s]\033[0m", truncateVisible(workerName, w-16)))
	lines = append(lines, "\033[38;2;80;80;80m Ctrl+W cycle \u2502 Esc exit\033[0m")

	// Log output with scroll
	if len(outputLog) == 0 {
		lines = append(lines, "\033[38;2;80;80;80;3m No output yet\033[0m")
		logH--
	} else {
		maxScroll := max(0, len(outputLog)-logH)
		if scroll > maxScroll {
			scroll = maxScroll
		}
		end := max(0, len(outputLog)-scroll)
		start := max(0, end-logH)
		for i := start; i < end; i++ {
			text := strings.TrimSpace(outputLog[i])
			if len(text) > w-2 {
				text = text[:w-2]
			}
			lines = append(lines, " \033[38;2;160;160;160m"+text+"\033[0m")
		}
		shown := end - start
		logH -= shown
	}

	// Pad remaining log area
	for i := 0; i < logH; i++ {
		lines = append(lines, "")
	}

	// Scroll indicator
	if scroll > 0 {
		indicator := fmt.Sprintf("\033[38;2;100;100;100m ↑ %d more lines \033[0m", scroll)
		lines = append(lines, truncateVisible(indicator, w))
	}

	// Input area
	for _, il := range inputLines {
		lines = append(lines, " "+truncateVisible(il, w-2))
	}

	return lines[:h]
}
