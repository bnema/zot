// Command zut is a lightweight terminal coding agent.
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/bnema/zut/packages/agent"
)

// Injected at build time via -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
// See .goreleaser.yaml for the release build and the Makefile for
// local builds. Defaults make `zut --version` print something sensible
// when built without ldflags.
var (
	// 0.0.0-dev is the placeholder for local / untagged builds.
	// Release builds replace it with the Git tag through ldflags.
	version = "0.0.0-dev"
	commit  = ""
	date    = ""
)

func main() {
	v := resolvedVersion(version)
	if commit != "" {
		short := commit
		if len(short) > 7 {
			short = short[:7]
		}
		v = v + " (" + short
		if date != "" {
			v = v + ", " + date
		}
		v = v + ")"
	}
	if err := agent.Run(os.Args[1:], v); err != nil {
		fmt.Fprintln(os.Stderr, "zut:", err)
		os.Exit(1)
	}
}

// resolvedVersion falls back to the module version embedded by Go when zut is
// installed with "go install ...@version". Release archives still use the
// version injected by GoReleaser, and local source builds remain 0.0.0-dev.
func resolvedVersion(linkedVersion string) string {
	info, _ := debug.ReadBuildInfo()
	return resolvedVersionFromBuildInfo(linkedVersion, info)
}

func resolvedVersionFromBuildInfo(linkedVersion string, info *debug.BuildInfo) string {
	if linkedVersion != "" && linkedVersion != "0.0.0" && linkedVersion != "0.0.0-dev" {
		return linkedVersion
	}
	if info == nil || info.Main.Version == "" || info.Main.Version == "(devel)" || isPseudoVersion(info.Main.Version) {
		return linkedVersion
	}
	return strings.TrimPrefix(info.Main.Version, "v")
}

var pseudoVersionPattern = regexp.MustCompile(`^v[0-9]+\.(0\.0-|[0-9]+\.[0-9]+-([^+]*\.)?0\.)[0-9]{14}-[A-Za-z0-9]+(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func isPseudoVersion(version string) bool {
	return pseudoVersionPattern.MatchString(version)
}
