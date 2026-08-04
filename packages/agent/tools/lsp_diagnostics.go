package tools

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/patriceckhart/zot/packages/agent/lsp"
	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

const writeDiagnosticsTimeout = 2 * time.Second

// CloseLSPManagers closes every manager referenced by the registry. Write,
// edit, and lsp share one manager in normal runs, so de-duplicate pointers
// before closing them. This is used by mode owners when an agent or registry
// is replaced; LSP processes should not outlive the session that created them.
func CloseLSPManagers(reg core.Registry) error {
	managers := make(map[*lsp.Manager]struct{})
	for _, tool := range reg {
		switch value := tool.(type) {
		case *LSPTool:
			if value != nil && value.Manager != nil {
				managers[value.Manager] = struct{}{}
			}
		case *WriteTool:
			if value != nil && value.LSP != nil {
				managers[value.LSP] = struct{}{}
			}
		case *EditTool:
			if value != nil && value.LSP != nil {
				managers[value.LSP] = struct{}{}
			}
		}
	}
	var firstErr error
	for manager := range managers {
		if err := manager.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// attachWriteDiagnostics performs a bounded post-write diagnostics pass. A
// failed or slow language server must never turn a successful filesystem
// mutation into a failed tool call, so callers only attach diagnostics that
// were actually returned before the short budget expired.
func attachWriteDiagnostics(ctx context.Context, cwd, path string, manager *lsp.Manager, result *core.ToolResult) {
	if manager == nil || result == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	diagnosticsCtx, cancel := context.WithTimeout(ctx, writeDiagnosticsTimeout)
	defer cancel()
	diagnostics, err := manager.Diagnostics(diagnosticsCtx, cwd, path)
	if err != nil {
		if details, ok := result.Details.(map[string]any); ok {
			status := "unavailable"
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				status = "timeout"
			case errors.Is(err, context.Canceled):
				status = "canceled"
			}
			details["diagnostics_status"] = status
			details["diagnostics_error"] = err.Error()
		}
		return
	}
	diagnostics = manager.ReduceDiagnostics(cwd, path, diagnostics)
	if len(diagnostics) == 0 {
		return
	}
	summary := lsp.SummarizeDiagnostics(diagnostics, cwd, 8)
	if strings.TrimSpace(summary) == "" || summary == "No diagnostics." {
		return
	}
	for index, block := range result.Content {
		text, ok := block.(provider.TextBlock)
		if !ok {
			continue
		}
		text.Text = strings.TrimRight(text.Text, "\n") + "\n\n" + summary + "\n"
		result.Content[index] = text
		break
	}
	if details, ok := result.Details.(map[string]any); ok {
		details["diagnostics"] = diagnostics
		details["diagnostics_summary"] = summary
	}
}
