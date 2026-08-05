package modes

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bnema/zut/packages/provider"
	"github.com/bnema/zut/packages/tui"
)

func TestLlamaDialogSearchesAsUserTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" || r.URL.Query().Get("search") != "qw" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `[{"id":"owner/qwen-GGUF","downloads":42}]`)
	}))
	defer server.Close()

	hf := provider.NewHuggingFaceClient("")
	hf.BaseURL = server.URL
	updated := make(chan struct{}, 1)
	d := newLlamaDialog()
	d.step = llamaSearch
	d.hf = hf
	d.invalidate = func() {
		select {
		case updated <- struct{}{}:
		default:
		}
	}
	d.HandleKey(tui.Key{Kind: tui.KeyRune, Rune: 'q'})
	d.HandleKey(tui.Key{Kind: tui.KeyRune, Rune: 'w'})

	select {
	case <-updated:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for live search")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.results) != 1 || d.results[0].ID != "owner/qwen-GGUF" {
		t.Fatalf("results = %#v", d.results)
	}
	if d.message != "" {
		t.Fatalf("message = %q", d.message)
	}
}

func TestLlamaDialogSearchAcceptsPastedText(t *testing.T) {
	d := newLlamaDialog()
	d.step = llamaSearch

	d.HandleKey(tui.Key{Kind: tui.KeyPaste, Paste: "unsloth/Qwen3.5-4B-GGUF\n"})
	defer d.Close()

	if d.query != "unsloth/Qwen3.5-4B-GGUF" {
		t.Fatalf("query = %q", d.query)
	}
}

func TestLlamaDialogSearchCursorTracksQuery(t *testing.T) {
	d := newLlamaDialog()
	d.step = llamaSearch
	d.query = "qwen"
	row, col := d.CursorPos()
	if row != 5 || col != 6 {
		t.Fatalf("cursor = (%d, %d)", row, col)
	}
	d.message = "searching Hugging Face..."
	row, _ = d.CursorPos()
	if row != 6 {
		t.Fatalf("cursor row with status = %d", row)
	}
}

func TestLlamaDialogLoadedModelRequiresUnloadConfirmation(t *testing.T) {
	d := newLlamaDialog()
	var loaded provider.LlamaCPPModel
	loaded.ID = "local-model"
	loaded.Status.Value = "loaded"
	d.step = llamaModels
	d.models = []provider.LlamaCPPModel{loaded}

	d.HandleKey(tui.Key{Kind: tui.KeyEnter})
	if d.step != llamaConfirmUnload || d.selected != "local-model" {
		t.Fatalf("step = %v, selected = %q", d.step, d.selected)
	}
	lines := d.Render(tui.Theme{}, 80)
	if !strings.Contains(strings.Join(lines, "\n"), "Unload local-model?") {
		t.Fatalf("render = %q", lines)
	}
}

func TestLlamaDialogRemovableModelRequiresDiskRemovalConfirmation(t *testing.T) {
	d := newLlamaDialog()
	var cached provider.LlamaCPPModel
	cached.ID = "owner/model:Q4_K_M"
	cached.Status.Value = "unloaded"
	cached.CanRemove = true
	d.step = llamaModels
	d.models = []provider.LlamaCPPModel{cached}

	d.HandleKey(tui.Key{Kind: tui.KeyRune, Rune: 'd'})
	if d.step != llamaConfirmRemove || d.selected != cached.ID {
		t.Fatalf("step = %v, selected = %q", d.step, d.selected)
	}
	lines := d.Render(tui.Theme{}, 80)
	if !strings.Contains(strings.Join(lines, "\n"), "Remove cached model "+cached.ID+"?") {
		t.Fatalf("render = %q", lines)
	}

	d.HandleKey(tui.Key{Kind: tui.KeyEsc})
	if d.step != llamaModels {
		t.Fatalf("step after escape = %v", d.step)
	}
}

func TestLlamaDialogDoesNotRemoveModelsDirEntry(t *testing.T) {
	d := newLlamaDialog()
	var local provider.LlamaCPPModel
	local.ID = "local-model"
	local.Status.Value = "unloaded"
	d.step = llamaModels
	d.models = []provider.LlamaCPPModel{local}

	d.HandleKey(tui.Key{Kind: tui.KeyRune, Rune: 'd'})
	if d.step != llamaModels {
		t.Fatalf("step = %v", d.step)
	}
	if !strings.Contains(d.message, "only router-downloaded cache models") {
		t.Fatalf("message = %q", d.message)
	}
}

func TestLlamaDialogDownloadSearchReturnsToModels(t *testing.T) {
	d := newLlamaDialog()
	d.step = llamaModels
	d.HandleKey(tui.Key{Kind: tui.KeyEnter})
	if d.step != llamaSearch {
		t.Fatalf("step = %v", d.step)
	}
	d.HandleKey(tui.Key{Kind: tui.KeyEsc})
	if d.step != llamaModels {
		t.Fatalf("step after escape = %v", d.step)
	}
}
