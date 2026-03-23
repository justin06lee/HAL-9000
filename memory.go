package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func memoryFilePath(projectName string) string {
	re := regexp.MustCompile(`[^a-z0-9\-]`)
	safe := re.ReplaceAllString(strings.ToLower(strings.TrimSpace(projectName)), "-")
	if safe == "" || safe == "-" {
		safe = "project"
	}
	safe = filepath.Base(safe)
	return filepath.Join(memoryDir, safe+".md")
}

func saveMemory(projectName, content string) error {
	fpath := memoryFilePath(projectName)
	entry := fmt.Sprintf("\n## %s\n%s\n", time.Now().Format(time.RFC3339), strings.TrimSpace(content))

	f, err := os.OpenFile(fpath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write header if new file
	info, _ := f.Stat()
	if info != nil && info.Size() == 0 {
		fmt.Fprintf(f, "# Project Memory: %s\n", projectName)
	}

	_, err = f.WriteString(entry)
	return err
}

func loadMemory(projectName string) string {
	fpath := memoryFilePath(projectName)
	data, err := os.ReadFile(fpath)
	if err != nil {
		return ""
	}
	return string(data)
}

func loadAllMemories() map[string]string {
	memories := make(map[string]string)
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		return memories
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(memoryDir, e.Name()))
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		memories[name] = string(data)
	}
	return memories
}

func deleteMemory(projectName string) {
	os.Remove(memoryFilePath(projectName))
}

// extractMemory parses <memory project="name">content</memory> from HAL responses.
func extractMemory(text string) (string, string) {
	re := regexp.MustCompile(`<memory project="([^"]+)">([\s\S]*?)</memory>`)
	m := re.FindStringSubmatch(text)
	if len(m) < 3 {
		return "", ""
	}
	return m[1], strings.TrimSpace(m[2])
}

// extractDiscontinue parses <discontinue project="name"/> from HAL responses.
func extractDiscontinue(text string) string {
	re := regexp.MustCompile(`<discontinue project="([^"]+)"\s*/>`)
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
