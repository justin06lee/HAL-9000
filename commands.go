package main

import "strings"

type slashCommand struct {
	name string
	arg  string
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
	case "discontinue", "remove", "delete":
		return &slashCommand{name: "discontinue", arg: arg}
	case "inspect", "view", "watch":
		return &slashCommand{name: "inspect", arg: arg}
	case "scan", "analyze", "look":
		return &slashCommand{name: "scan", arg: arg}
	default:
		return nil
	}
}
