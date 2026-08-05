package main

import (
	"runtime/debug"
	"testing"
)

func TestResolvedVersionFromBuildInfo(t *testing.T) {
	tests := []struct {
		name          string
		linkedVersion string
		moduleVersion string
		want          string
	}{
		{
			name:          "go install release",
			linkedVersion: "0.0.0",
			moduleVersion: "v0.2.94",
			want:          "0.2.94",
		},
		{
			name:          "go install release from dev placeholder",
			linkedVersion: "0.0.0-dev",
			moduleVersion: "v0.2.94",
			want:          "0.2.94",
		},
		{
			name:          "local pseudo-version keeps dev placeholder",
			linkedVersion: "0.0.0-dev",
			moduleVersion: "v0.2.94-0.20260804145622-bce908a66c9a",
			want:          "0.0.0-dev",
		},
		{
			name:          "release linker version wins",
			linkedVersion: "0.2.95",
			moduleVersion: "v0.2.94",
			want:          "0.2.95",
		},
		{
			name:          "local build",
			linkedVersion: "0.0.0",
			moduleVersion: "(devel)",
			want:          "0.0.0",
		},
		{
			name:          "local dev build",
			linkedVersion: "0.0.0-dev",
			moduleVersion: "(devel)",
			want:          "0.0.0-dev",
		},
		{
			name:          "missing build info",
			linkedVersion: "0.0.0",
			want:          "0.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var info *debug.BuildInfo
			if tt.moduleVersion != "" {
				info = &debug.BuildInfo{Main: debug.Module{Version: tt.moduleVersion}}
			}
			if got := resolvedVersionFromBuildInfo(tt.linkedVersion, info); got != tt.want {
				t.Fatalf("resolvedVersionFromBuildInfo(%q, module %q) = %q, want %q", tt.linkedVersion, tt.moduleVersion, got, tt.want)
			}
		})
	}
}
