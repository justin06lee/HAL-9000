package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Key files to look for and read (config, entry points, docs)
var keyFiles = []string{
	"README.md", "README", "readme.md",
	"package.json", "go.mod", "Cargo.toml", "pyproject.toml",
	"requirements.txt", "Gemfile", "pom.xml", "build.gradle",
	"Makefile", "Dockerfile", "docker-compose.yml", "docker-compose.yaml",
	".env.example", "tsconfig.json", "vite.config.ts", "next.config.js",
	"CLAUDE.md",
}

// Source extensions to sample
var sourceExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true,
	".jsx": true, ".rs": true, ".java": true, ".rb": true, ".c": true,
	".cpp": true, ".h": true, ".swift": true, ".kt": true, ".cs": true,
	".vue": true, ".svelte": true, ".php": true,
}

// scanRepo analyzes a repository and returns a context string for HAL.
func scanRepo(repoPath string) (string, error) {
	info, err := os.Stat(repoPath)
	if err != nil {
		return "", fmt.Errorf("cannot access path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", repoPath)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Repository Analysis: %s\n\n", repoPath))

	// 1. Directory tree (max 5 levels deep, max 500 entries)
	sb.WriteString("## Directory Structure\n```\n")
	count := 0
	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || count >= 500 {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(repoPath, path)
		if rel == "." {
			return nil
		}

		// Skip hidden dirs, node_modules, vendor, .git, __pycache__, etc.
		base := filepath.Base(rel)
		if strings.HasPrefix(base, ".") || base == "node_modules" ||
			base == "vendor" || base == "__pycache__" || base == ".git" ||
			base == "dist" || base == "build" || base == "target" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		depth := strings.Count(rel, string(filepath.Separator))
		if depth > 5 {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		indent := strings.Repeat("  ", depth)
		if info.IsDir() {
			sb.WriteString(fmt.Sprintf("%s%s/\n", indent, base))
		} else {
			sb.WriteString(fmt.Sprintf("%s%s\n", indent, base))
		}
		count++
		return nil
	})
	sb.WriteString("```\n\n")

	// 2. Read key files
	for _, name := range keyFiles {
		fpath := filepath.Join(repoPath, name)
		data, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		content := string(data)
		if len(content) > 5000 {
			content = content[:5000] + "\n... (truncated)"
		}
		sb.WriteString(fmt.Sprintf("## %s\n```\n%s\n```\n\n", name, content))
	}

	// 3. Sample source files (first 15 found, first 150 lines each)
	sb.WriteString("## Source File Samples\n\n")
	sampled := 0
	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || sampled >= 15 || info.IsDir() {
			if info != nil && info.IsDir() {
				base := filepath.Base(path)
				if strings.HasPrefix(base, ".") || base == "node_modules" ||
					base == "vendor" || base == "__pycache__" ||
					base == "dist" || base == "build" || base == "target" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		ext := filepath.Ext(path)
		if !sourceExts[ext] {
			return nil
		}

		rel, _ := filepath.Rel(repoPath, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		preview := lines
		if len(preview) > 150 {
			preview = preview[:150]
		}

		sb.WriteString(fmt.Sprintf("### %s\n```\n%s\n```\n\n", rel, strings.Join(preview, "\n")))
		sampled++
		return nil
	})

	// 4. Git info if available
	gitDir := filepath.Join(repoPath, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		sb.WriteString("## Git Repository\nThis is a git repository.\n\n")
		// Read last commit message from HEAD if available
		headFile := filepath.Join(gitDir, "HEAD")
		if data, err := os.ReadFile(headFile); err == nil {
			sb.WriteString(fmt.Sprintf("HEAD: %s\n", strings.TrimSpace(string(data))))
		}
	}

	return sb.String(), nil
}

// resolvePath expands ~ and resolves relative paths against cwd.
func resolvePath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		// Resolve relative to cwd (e.g. /projectA becomes cwd/projectA)
		p = filepath.Join(baseDir, p)
	}
	return filepath.Clean(p)
}

// detectRepoPath tries to find a file path in user text that looks like a repo reference.
func detectRepoPath(text string) string {
	words := strings.Fields(text)
	for _, w := range words {
		// Strip trailing punctuation
		w = strings.TrimRight(w, ".,;:!?\"')")
		if w == "" || w == "/" || w == "." {
			continue
		}

		// Match paths: /something, ~/something, ./something, ../something
		if strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~/") ||
			strings.HasPrefix(w, "./") || strings.HasPrefix(w, "../") {
			resolved := resolvePath(w)
			if info, err := os.Stat(resolved); err == nil && info.IsDir() {
				return resolved
			}
		}
	}
	return ""
}
