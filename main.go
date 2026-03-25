package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
)

const version = "0.2.0"

func main() {
	// Parse flags before anything else
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--version", "-v", "version":
			fmt.Printf("hal9000 v%s\n", version)
			return
		case "--help", "-h", "help":
			fmt.Println("HAL 9000 — AI Project Orchestrator")
			fmt.Printf("Version: %s\n\n", version)
			fmt.Println("Usage: hal9000 [options]")
			fmt.Println("")
			fmt.Println("Options:")
			fmt.Println("  --model <model>       Claude model (e.g. sonnet, opus, haiku)")
			fmt.Println("  --thinking <level>    Effort level (low, medium, high, max)")
			fmt.Println("  --version             Show version")
			fmt.Println("")
			fmt.Println("Commands inside HAL:")
			fmt.Println("  /scan <path>         Scan a repo for HAL to analyze")
			fmt.Println("  /discontinue <name>  Remove a project")
			fmt.Println("  /inspect <name>      View a worker's output")
			fmt.Println("  /search <query>      Search project memory (RAG)")
			fmt.Println("  /memory <project>    Show stored memory topics")
			fmt.Println("  /new                 Start a fresh session")
			fmt.Println("")
			fmt.Println("Keybindings:")
			fmt.Println("  Ctrl+B  Start build")
			fmt.Println("  Ctrl+W  Cycle worker inspection")
			fmt.Println("  Ctrl+Y  Copy last HAL response")
			fmt.Println("  Ctrl+Q  Quit")
			return
		case "--model":
			if i+1 < len(args) {
				i++
				claudeModel = args[i]
			}
		case "--thinking":
			if i+1 < len(args) {
				i++
				thinkingBudget = args[i]
			}
		}
	}

	p := tea.NewProgram(
		newModel(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
