package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// TurnStatus is the durable outcome of one delegated turn.
type TurnStatus string

const (
	ResultSucceeded TurnStatus = "succeeded"
	ResultFailed    TurnStatus = "failed"
	ResultCanceled  TurnStatus = "canceled"
)

// ArtifactRef points to a durable file without exposing the supervisor's
// local state layout to callers.
type ArtifactRef struct {
	Name      string `json:"name,omitempty"`
	Ref       string `json:"ref"`
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

// ResultError is intentionally free of credentials and argv details.
type ResultError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// TurnResult is the protocol-level result for a delegated turn. Output is a
// bounded inline preview; the full session/history remains available through
// HistoryRef and the complete JSON result through ResultRef.
type TurnResult struct {
	Version      int             `json:"version"`
	AgentID      string          `json:"agent_id,omitempty"`
	TurnID       string          `json:"turn_id,omitempty"`
	Status       TurnStatus      `json:"status"`
	Summary      string          `json:"summary,omitempty"`
	Output       string          `json:"output,omitempty"`
	Structured   json.RawMessage `json:"structured,omitempty"`
	Artifacts    []ArtifactRef   `json:"artifacts,omitempty"`
	ChangedFiles []string        `json:"changed_files,omitempty"`
	Usage        map[string]any  `json:"usage,omitempty"`
	Error        *ResultError    `json:"error,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

func (r *TurnResult) Validate(maxBytes, maxLines int) error {
	if r == nil {
		return errors.New("subagents: nil turn result")
	}
	if r.Version == 0 {
		r.Version = ProtocolVersion
	}
	if r.Version != ProtocolVersion {
		return fmt.Errorf("subagents: unsupported result version %d", r.Version)
	}
	if r.Status != ResultSucceeded && r.Status != ResultFailed && r.Status != ResultCanceled {
		return fmt.Errorf("subagents: invalid result status %q", r.Status)
	}
	if r.AgentID == "" {
		return errors.New("subagents: result missing agent id")
	}
	if r.TurnID == "" {
		return errors.New("subagents: result missing turn id")
	}
	if len(r.Structured) > 0 && !json.Valid(r.Structured) {
		return errors.New("subagents: result structured payload is invalid JSON")
	}
	if maxBytes > 0 && len([]byte(r.Output)) > maxBytes {
		return fmt.Errorf("subagents: result output exceeds %d bytes", maxBytes)
	}
	if maxLines > 0 && countLines(r.Output) > maxLines {
		return fmt.Errorf("subagents: result output exceeds %d lines", maxLines)
	}
	return nil
}

// Bounded returns a copy with inline output capped by bytes and lines. It
// never mutates the durable result's caller-owned value.
func (r *TurnResult) Bounded(maxBytes, maxLines int) *TurnResult {
	out := cloneTurnResult(r)
	if out == nil {
		return nil
	}
	const marker = "...[output truncated]"
	if len([]byte(out.Summary)) > 4*1024 {
		out.Summary = truncateUTF8(out.Summary, 4*1024)
	}
	if maxLines > 0 {
		lines := strings.Split(out.Output, "\n")
		if len(lines) > maxLines {
			if maxLines == 1 {
				out.Output = marker
			} else {
				out.Output = strings.Join(lines[:maxLines-1], "\n") + "\n" + marker
			}
		}
	}
	if maxBytes > 0 && len([]byte(out.Output)) > maxBytes {
		if maxBytes <= len(marker) {
			out.Output = truncateUTF8(out.Output, maxBytes)
		} else {
			out.Output = truncateUTF8(out.Output, maxBytes-len(marker)) + marker
		}
	}
	return out
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	data := []byte(value)
	if len(data) <= maxBytes {
		return value
	}
	data = data[:maxBytes]
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data)
}

func validateTurnResultAgent(result *TurnResult, agentID string) error {
	if result == nil {
		return errors.New("subagents: nil turn result")
	}
	if agentID != "" && result.AgentID != agentID {
		return fmt.Errorf("subagents: turn result belongs to agent %q, want %q", result.AgentID, agentID)
	}
	return nil
}

func decodeTurnResultEvent(ev Event, agentID string, maxBytes, maxLines int) (*TurnResult, error) {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return nil, fmt.Errorf("subagents: encode turn result event: %w", err)
	}
	var result TurnResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("subagents: decode turn result event: %w", err)
	}
	if result.AgentID == "" {
		result.AgentID = firstNonEmpty(ev.AgentID, agentID)
	}
	if err := validateTurnResultAgent(&result, agentID); err != nil {
		return nil, err
	}
	if result.TurnID == "" {
		result.TurnID = ev.TurnID
	}
	if result.Version == 0 {
		result.Version = ProtocolVersion
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = ev.Time
		if result.CreatedAt.IsZero() {
			result.CreatedAt = time.Now().UTC()
		}
	}
	resultPtr := result.Bounded(maxBytes, maxLines)
	if err := resultPtr.Validate(0, 0); err != nil {
		return nil, err
	}
	return resultPtr, nil
}

func cloneTurnResult(r *TurnResult) *TurnResult {
	if r == nil {
		return nil
	}
	copy := *r
	copy.Structured = append(json.RawMessage(nil), r.Structured...)
	copy.Artifacts = append([]ArtifactRef(nil), r.Artifacts...)
	copy.ChangedFiles = append([]string(nil), r.ChangedFiles...)
	if r.Usage != nil {
		copy.Usage = make(map[string]any, len(r.Usage))
		for key, value := range r.Usage {
			copy.Usage[key] = value
		}
	}
	if r.Error != nil {
		errCopy := *r.Error
		copy.Error = &errCopy
	}
	return &copy
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return 1 + strings.Count(s, "\n")
}

func AgentRef(id string) string   { return "subagent://" + id }
func HistoryRef(id string) string { return AgentRef(id) + "/history" }
func ResultRef(id string) string  { return AgentRef(id) + "/result" }
func PatchRef(id string) string   { return AgentRef(id) + "/patch" }

const maxResultFileBytes = 8 * 1024 * 1024

func resultPath(stateDir string) string { return filepath.Join(stateDir, "result.json") }
func patchPath(stateDir string) string  { return filepath.Join(stateDir, "patch.diff") }

func writeTurnResult(stateDir string, result *TurnResult) error {
	if result == nil {
		return errors.New("subagents: nil result")
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now().UTC()
	}
	if result.Version == 0 {
		result.Version = ProtocolVersion
	}
	if err := result.Validate(0, 0); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("subagents result marshal: %w", err)
	}
	if len(data) > maxResultFileBytes {
		return fmt.Errorf("subagents result exceeds %d bytes", maxResultFileBytes)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("subagents result dir: %w", err)
	}
	tmp := resultPath(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("subagents result write: %w", err)
	}
	if err := os.Rename(tmp, resultPath(stateDir)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("subagents result rename: %w", err)
	}
	return nil
}

func (f *Swarm) captureWorkspace(a *Agent) {
	if a == nil || a.workspaceCapture == nil {
		return
	}
	stateDir := a.stateDirectory(f.cfg.Root)
	if _, err := os.Stat(stateDir); err != nil {
		return
	}
	capture, err := a.workspaceCapture()
	if err != nil {
		return
	}
	a.lifecycleMu.Lock()
	a.changedFiles = append([]string(nil), capture.ChangedFiles...)
	a.lifecycleMu.Unlock()
	if len(capture.Patch) == 0 {
		return
	}
	path := patchPath(a.stateDirectory(f.cfg.Root))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, capture.Patch, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return
	}
	a.lifecycleMu.Lock()
	a.patchRef = PatchRef(a.ID)
	a.lifecycleMu.Unlock()
}

func (f *Swarm) ensureResult(a *Agent, status Status, runErr error) {
	if a == nil {
		return
	}
	result := a.Result()
	if result != nil {
		if err := validateTurnResultAgent(result, a.ID); err != nil {
			result = nil
		}
	}
	if result != nil {
		// A child may omit agent/turn metadata in a result event; the
		// supervisor supplies it before validating the durable fallback.
		if result.AgentID == "" {
			result.AgentID = a.ID
		}
		if result.TurnID == "" {
			result.TurnID = firstNonEmpty(a.CurrentTurnID(), fmt.Sprintf("turn-%d", a.Attempt))
		}
		if result.Version == 0 {
			result.Version = ProtocolVersion
		}
		if result.CreatedAt.IsZero() {
			result.CreatedAt = time.Now().UTC()
		}
		if err := result.Validate(0, 0); err != nil {
			result = nil
		}
	}
	if result == nil {
		turnID := a.CurrentTurnID()
		if turnID == "" {
			turnID = fmt.Sprintf("turn-%d", a.Attempt)
		}
		a.mu.Lock()
		output := a.lastAssistant
		a.mu.Unlock()
		resultStatus := ResultSucceeded
		switch {
		case status == StatusFailed && errors.Is(runErr, context.Canceled):
			resultStatus = ResultCanceled
		case status == StatusFailed:
			resultStatus = ResultFailed
		case status == StatusKilled:
			resultStatus = ResultCanceled
		}
		result = &TurnResult{
			Version:   ProtocolVersion,
			AgentID:   a.ID,
			TurnID:    turnID,
			Status:    resultStatus,
			Output:    output,
			CreatedAt: time.Now().UTC(),
		}
	}
	if result.AgentID == "" {
		result.AgentID = a.ID
	}
	if result.TurnID == "" {
		result.TurnID = firstNonEmpty(a.CurrentTurnID(), fmt.Sprintf("turn-%d", a.Attempt))
	}
	if result.Version == 0 {
		result.Version = ProtocolVersion
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now().UTC()
	}
	if runErr != nil && result.Error == nil {
		result.Error = &ResultError{Code: "runner_failed", Message: truncate(runErr.Error(), 500)}
	}
	if len(result.ChangedFiles) == 0 {
		a.lifecycleMu.Lock()
		result.ChangedFiles = append([]string(nil), a.changedFiles...)
		a.lifecycleMu.Unlock()
	}
	if result.Summary == "" && result.Output != "" {
		result.Summary = strings.Split(strings.TrimSpace(result.Output), "\n")[0]
	}
	result = result.Bounded(a.maxOutputBytes, a.maxOutputLines)
	a.setResult(result)
	stateDir := a.stateDirectory(f.cfg.Root)
	if _, err := os.Stat(stateDir); err == nil {
		if err := writeTurnResult(stateDir, result); err == nil {
			a.lifecycleMu.Lock()
			a.resultRef = ResultRef(a.ID)
			a.lifecycleMu.Unlock()
		}
	}
}

// ReadResult returns the durable structured result for an agent.
func (f *Swarm) ReadResult(id string) (*TurnResult, error) {
	a := f.Get(id)
	if a == nil {
		return nil, fmt.Errorf("subagents: no such agent %q", id)
	}
	if result := a.Result(); result != nil {
		if err := validateTurnResultAgent(result, a.ID); err != nil {
			return nil, err
		}
		return result, nil
	}
	result, err := readTurnResult(a.stateDirectory(f.cfg.Root))
	if err != nil {
		return nil, err
	}
	if err := validateTurnResultAgent(result, a.ID); err != nil {
		return nil, err
	}
	return result.Bounded(f.cfg.Policy.MaxOutputBytes, f.cfg.Policy.MaxOutputLines), nil
}

func (f *Swarm) ResultReference(id string) string  { return ResultRef(id) }
func (f *Swarm) HistoryReference(id string) string { return HistoryRef(id) }
func (f *Swarm) PatchReference(id string) string   { return PatchRef(id) }

func readTurnResult(stateDir string) (*TurnResult, error) {
	file, err := os.Open(resultPath(stateDir))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxResultFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResultFileBytes {
		return nil, fmt.Errorf("subagents result exceeds %d bytes", maxResultFileBytes)
	}
	var result TurnResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("subagents result parse: %w", err)
	}
	if err := result.Validate(0, 0); err != nil {
		return nil, err
	}
	return &result, nil
}
