// Package audit writes structured (JSON-lines) records of every action
// this connector takes against Teams/Graph or Trello: who/what did it, what
// action, against what target, with what result. One line per event, so it
// greps/parses trivially and can be shipped to a real log sink later
// (Log Analytics, a SIEM) without changing the call sites here.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	Time      string `json:"time"`
	Principal string `json:"principal"` // "delegated:<flow>" for a user session, or the service app's client ID
	Action    string `json:"action"`    // e.g. "graph.read_channel", "trello.create_card"
	Target    string `json:"target"`    // team/channel ID, Trello card name, etc.
	Result    string `json:"result"`    // "ok" or an error summary
}

var (
	mu      sync.Mutex
	logPath string
)

func path() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if logPath != "" {
		return logPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".teams-mcp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	logPath = filepath.Join(dir, "audit.log")
	return logPath, nil
}

// Log appends one structured audit entry. Failures to write the audit log
// are swallowed (best-effort) rather than failing the calling operation —
// an audit log outage shouldn't take down the tool it's observing.
func Log(principal, action, target string, err error) {
	p, pathErr := path()
	if pathErr != nil {
		return
	}
	result := "ok"
	if err != nil {
		result = err.Error()
	}
	entry := Entry{
		Time:      time.Now().UTC().Format(time.RFC3339),
		Principal: principal,
		Action:    action,
		Target:    target,
		Result:    result,
	}
	data, marshalErr := json.Marshal(entry)
	if marshalErr != nil {
		return
	}
	data = append(data, '\n')

	mu.Lock()
	defer mu.Unlock()
	f, openErr := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if openErr != nil {
		return
	}
	defer f.Close()
	f.Write(data)
}
