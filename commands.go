package main

import "strings"

type slashCommand struct {
	name string
	arg  string
}

// commandArgCount returns how many argument words a command expects (0 or 1).
func commandArgCount(name string) int {
	switch name {
	case "scan", "discontinue", "inspect", "search", "memory", "forget":
		return 1
	default:
		return 0
	}
}

// parseCommand checks if text is a slash command. Returns nil if not.
func parseCommand(text string) *slashCommand {
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
	case "scan":
		return &slashCommand{name: "scan", arg: arg}
	case "discontinue":
		return &slashCommand{name: "discontinue", arg: arg}
	case "inspect":
		return &slashCommand{name: "inspect", arg: arg}
	case "search":
		return &slashCommand{name: "search", arg: arg}
	case "memory":
		return &slashCommand{name: "memory", arg: arg}
	case "forget":
		return &slashCommand{name: "forget", arg: arg}
	case "model":
		return &slashCommand{name: "model", arg: arg}
	case "new":
		return &slashCommand{name: "new", arg: arg}
	case "commands":
		return &slashCommand{name: "commands", arg: arg}
	case "clear":
		return &slashCommand{name: "clear", arg: arg}
	case "questions", "q":
		return &slashCommand{name: "questions", arg: arg}
	default:
		return nil
	}
}
