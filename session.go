package main

import (
	"encoding/json"
	"os"
	"time"
)

type sessionData struct {
	HALSessionID string               `json:"hal_session_id"`
	BuildStarted bool                 `json:"build_started"`
	Projects     []projectSessionData `json:"projects"`
	Workers      []workerSessionData  `json:"workers"`
	SavedAt      string               `json:"saved_at"`
}

type projectSessionData struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoPath    string `json:"repo_path"`
	SpecFile    string `json:"spec_file"`
}

type workerSessionData struct {
	Name      string `json:"name"`
	RepoPath  string `json:"repo_path"`
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
	IsAudit   bool   `json:"is_audit"`
}

func saveSession(m *model) {
	sd := sessionData{
		HALSessionID: m.conversation.sessionID,
		BuildStarted: m.buildStarted,
		SavedAt:      time.Now().Format(time.RFC3339),
	}

	for _, p := range m.projectSpecs {
		sd.Projects = append(sd.Projects, projectSessionData{
			Name:        p.name,
			Description: p.description,
			RepoPath:    p.repoPath,
			SpecFile:    p.filePath,
		})
	}

	m.orch.mu.RLock()
	for _, w := range m.orch.workers {
		w.mu.Lock()
		sd.Workers = append(sd.Workers, workerSessionData{
			Name:      w.name,
			RepoPath:  w.repoPath,
			Status:    w.status.String(),
			SessionID: w.sessionID,
			IsAudit:   w.isAudit,
		})
		w.mu.Unlock()
	}
	m.orch.mu.RUnlock()

	data, err := json.MarshalIndent(sd, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(sessionFile, data, 0o644)
}

func loadSession() *sessionData {
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		return nil
	}
	var sd sessionData
	if err := json.Unmarshal(data, &sd); err != nil {
		return nil
	}
	return &sd
}

func statusFromString(s string) workerStatus {
	switch s {
	case "pending":
		return statusPending
	case "running":
		return statusRunning
	case "waiting":
		return statusWaiting
	case "completed":
		return statusCompleted
	case "auditing":
		return statusAuditing
	case "done":
		return statusDone
	case "failed":
		return statusFailed
	case "audit_failed":
		return statusAuditFailed
	}
	return statusPending
}
