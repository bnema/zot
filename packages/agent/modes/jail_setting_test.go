package modes

import (
	"testing"

	"github.com/bnema/zut/packages/agent/tools"
)

func TestJailByDefaultSettingUpdatesLiveSandbox(t *testing.T) {
	sandbox := tools.NewSandbox(t.TempDir())
	i := &Interactive{
		cfg: InteractiveConfig{Sandbox: sandbox},
	}

	i.applySettingToggle("jail_by_default", true)
	if !sandbox.Locked() {
		t.Fatal("enabling jail by default did not lock the live sandbox")
	}
	if i.cfg.JailByDefault == nil || !*i.cfg.JailByDefault {
		t.Fatal("interactive config did not record enabled jail default")
	}

	i.applySettingToggle("jail_by_default", false)
	if sandbox.Locked() {
		t.Fatal("disabling jail by default did not unlock the live sandbox")
	}
	if i.cfg.JailByDefault == nil || *i.cfg.JailByDefault {
		t.Fatal("interactive config did not record disabled jail default")
	}
}
