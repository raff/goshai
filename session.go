package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultSessionName = "last"

func sessionsDir() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

func validateSessionName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return fmt.Errorf("invalid session name %q; path separators are not allowed", name)
	}
	return nil
}

func sessionPath(name string) (string, error) {
	if err := validateSessionName(name); err != nil {
		return "", err
	}
	dir, err := sessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

// LoadSession reads a session by name; returns nil slice for a new session.
func LoadSession(name string) ([]Message, error) {
	path, err := sessionPath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var messages []Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// SaveSession writes session messages to the platform config sessions directory.
func SaveSession(name string, messages []Message) error {
	path, err := sessionPath(name)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// SessionInfo holds display metadata for a session.
type SessionInfo struct {
	Name     string
	Messages int
	Modified time.Time
}

// ListSessions returns metadata for all saved sessions.
func ListSessions() ([]SessionInfo, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sessions []SessionInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		info, err := e.Info()
		if err != nil {
			continue
		}
		msgs, err := LoadSession(name)
		if err != nil {
			continue
		}
		sessions = append(sessions, SessionInfo{
			Name:     name,
			Messages: len(msgs),
			Modified: info.ModTime(),
		})
	}
	return sessions, nil
}

// RenameSession renames the "last" session to a new name; errors if target exists.
func RenameSession(from, to string) error {
	fromPath, err := sessionPath(from)
	if err != nil {
		return err
	}
	toPath, err := sessionPath(to)
	if err != nil {
		return err
	}
	if _, err := os.Stat(fromPath); os.IsNotExist(err) {
		return fmt.Errorf("session %q not found", from)
	}
	if _, err := os.Stat(toPath); err == nil {
		return fmt.Errorf("session %q already exists; delete it first", to)
	}
	return os.Rename(fromPath, toPath)
}
