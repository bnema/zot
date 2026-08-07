package modes

import (
	"testing"

	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
)

func TestHandleEventUpdatesContextUsageFromCacheOnlyTurn(t *testing.T) {
	interactive := NewInteractive(InteractiveConfig{})
	interactive.lastCtxInput = 99

	interactive.handleEvent(core.EvUsage{Usage: provider.Usage{
		CacheReadTokens:  400,
		CacheWriteTokens: 50,
	}})

	if got := interactive.lastCtxInput; got != 450 {
		t.Fatalf("context usage = %d, want 450", got)
	}

	interactive.handleEvent(core.EvUsage{})
	if got := interactive.lastCtxInput; got != 450 {
		t.Fatalf("empty usage cleared context usage to %d, want 450", got)
	}
}
