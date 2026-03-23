package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.2.0"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Printf("hal9000 v%s\n", version)
			return
		case "--help", "-h", "help":
			fmt.Println("HAL 9000 — AI Project Orchestrator")
			fmt.Printf("Version: %s\n\n", version)
			fmt.Println("Usage: hal9000            Start HAL in the current directory")
			fmt.Println("       hal9000 --version  Show version")
			fmt.Println("")
			fmt.Println("Run hal9000 from your workspace directory.")
			fmt.Println("Relative paths like /projectA resolve against your cwd.")
			fmt.Println("")
			fmt.Println("Commands inside HAL:")
			fmt.Println("  /scan <path>         Scan a repo for HAL to analyze")
			fmt.Println("  /discontinue <name>  Remove a project")
			fmt.Println("  /inspect <name>      View a worker's output")
			fmt.Println("")
			fmt.Println("Keybindings:")
			fmt.Println("  Ctrl+B  Start build")
			fmt.Println("  Ctrl+W  Cycle worker inspection")
			fmt.Println("  Ctrl+Y  Copy last HAL response")
			fmt.Println("  Ctrl+Q  Quit")
			return
		}
	}

	p := tea.NewProgram(
		newModel(),
		tea.WithAltScreen(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
