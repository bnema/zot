package modes

import (
	"testing"

	"github.com/patriceckhart/zot/packages/tui"
)

func TestRenderUpdateBannerOmitsUnavailableUpdate(t *testing.T) {
	if lines := renderUpdateBanner(tui.Theme{}, UpdateInfo{}, 80); len(lines) != 0 {
		t.Fatalf("banner = %#v, want no banner", lines)
	}
}
