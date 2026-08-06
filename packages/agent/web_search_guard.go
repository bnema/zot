package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/bnema/zut/packages/core"
)

var errWebSearchSessionRevoked = errors.New("web_search is unavailable in this session")

// webSearchSessionGuard is shared by every web_search tool instance built for
// one interactive session. Registry refreshes replace the advertised tool, but
// an older tool already snapshotted by core still consults this guard at its
// execution boundary.
type webSearchSessionGuard struct {
	available  atomic.Bool
	generation atomic.Uint64
}

func (g *webSearchSessionGuard) setAvailable(available bool) {
	if g == nil {
		return
	}
	if !available {
		// Increment only on an actual allow-to-deny transition. A wrapper built
		// while denied then committed by a later successful refresh belongs to
		// the new generation; wrappers from before revocation never revive.
		if g.available.Swap(false) {
			g.generation.Add(1)
		}
		return
	}
	g.available.Store(true)
}

func (g *webSearchSessionGuard) wrapRegistry(reg core.Registry) core.Registry {
	if g == nil || reg == nil {
		return reg
	}
	tool, ok := reg["web_search"]
	if !ok {
		return reg
	}
	if guarded, ok := tool.(*guardedWebSearchTool); ok && guarded.guard == g {
		return reg
	}
	reg["web_search"] = &guardedWebSearchTool{
		Tool:       tool,
		guard:      g,
		generation: g.generation.Load(),
	}
	return reg
}

// guardedWebSearchTool deliberately wraps only web_search. All extension
// interception, confirmation, and permission behavior remains in the normal
// core path; this final check only closes the stale-registry execution window.
type guardedWebSearchTool struct {
	core.Tool
	guard      *webSearchSessionGuard
	generation uint64
}

func (t *guardedWebSearchTool) available() bool {
	return t != nil && t.guard != nil && t.guard.available.Load() && t.generation == t.guard.generation.Load()
}

func (t *guardedWebSearchTool) Preview(ctx context.Context, args json.RawMessage) (core.ToolResult, error) {
	if !t.available() {
		return core.ToolResult{}, errWebSearchSessionRevoked
	}
	previewer, ok := t.Tool.(core.ToolPreviewer)
	if !ok {
		return core.ToolResult{}, errors.New("web_search preview is unavailable")
	}
	return previewer.Preview(ctx, args)
}

func (t *guardedWebSearchTool) Execute(ctx context.Context, args json.RawMessage, progress func(string)) (core.ToolResult, error) {
	if !t.available() {
		return core.ToolResult{}, errWebSearchSessionRevoked
	}
	return t.Tool.Execute(ctx, args, progress)
}
