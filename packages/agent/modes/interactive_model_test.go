package modes

import (
	"context"
	"testing"
)

func TestModelPickerSkipsLlamaRefreshWhenRouterIsNotConfigured(t *testing.T) {
	i := &Interactive{
		cfg: InteractiveConfig{
			RefreshLlamaCPPModels: func(context.Context) error { return nil },
		},
		modelRefresh: make(chan modelRefreshResult, 1),
		modelDialog:  newModelDialog(),
	}

	i.runSlash(context.Background(), "/model")

	if !i.modelDialog.Active() {
		t.Fatal("model picker did not open immediately")
	}
	if i.statusOK == "refreshing models" {
		t.Fatal("refresh status shown without llama.cpp router configuration")
	}
}
