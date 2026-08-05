package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIsDevVersion(t *testing.T) {
	for _, version := range []string{"", "dev", "0.0.0", "0.0.0-dev", "0.0.0-dev (abc123, today)", "0.0.0-dev+local"} {
		if !isDevVersion(version) {
			t.Errorf("isDevVersion(%q) = false, want true", version)
		}
	}
	for _, version := range []string{"0.0.1", "1.0.0", "0.0.0-rc1"} {
		if isDevVersion(version) {
			t.Errorf("isDevVersion(%q) = true, want false", version)
		}
	}
}

func TestCheckForUpdateSkipsDevVersionWithoutCacheOrNetwork(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	info := CheckForUpdate(ctx, home, "0.0.0-dev")
	if info != (UpdateInfo{}) {
		t.Fatalf("development check = %+v, want an empty result", info)
	}
	if _, err := os.Stat(filepath.Join(home, updateCheckFile)); !os.IsNotExist(err) {
		t.Fatalf("development check wrote a cache file: %v", err)
	}
}
