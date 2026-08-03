//go:build linux

package tools

import (
	"context"
	"os/exec"
	"sync"
	"time"
)

var systemdRunPath = sync.OnceValue(detectSystemdRun)

func platformShellInvocation(shell shellCommand, command string) shellInvocation {
	if path := systemdRunPath(); path != "" {
		return systemdScopeInvocation(path, shell, command)
	}
	return directShellInvocation(shell, command)
}

// detectSystemdRun verifies both that systemd-run is installed and that the
// user's service manager accepts transient scopes with OOMPolicy. A binary can
// be present in containers and minimal sessions where no user manager exists.
func detectSystemdRun() string {
	path, err := exec.LookPath("systemd-run")
	if err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	probe := systemdScopeInvocation(path, shellCommand{path: "/bin/sh", flag: "-c"}, ":")
	if err := exec.CommandContext(ctx, probe.path, probe.args...).Run(); err != nil {
		return ""
	}
	return path
}
