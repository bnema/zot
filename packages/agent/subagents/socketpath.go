package subagents

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// maxUnixSocketPath is the conservative platform-portable path limit
// for unix sockets. macOS allows 104, linux 108 (including the NUL
// terminator). We pick 100 so the path itself plus a small filename
// tail stays under both caps with a safety margin.
const maxUnixSocketPath = 100

// inboxSocketPath returns a per-agent unix-socket path in a transient
// runtime directory. Durable subagent state can live on filesystems such as
// NFS, 9p, or shared VM mounts that support regular files but reject unix
// socket nodes, so the socket must not be placed below root.
//
// Candidate directories are tried in this order:
//
//  1. $XDG_RUNTIME_DIR, when absolute;
//  2. the platform temporary directory; and
//  3. /tmp as a Unix fallback.
//
// Each candidate is probed with a real unix listener before it is selected.
// The root hash keeps separate zut homes from colliding while preserving a
// stable path across parent and child processes and across Resume calls.
func inboxSocketPath(root, agentID string) (string, error) {
	if runtime.GOOS == "windows" {
		// Keep subagent construction usable for callers with custom runners.
		// The production listener still reports that its unix transport is
		// unavailable, as it did before runtime-directory probing was added.
		return filepath.Join(root, "agents", agentID, "in.sock"), nil
	}

	var bases []string
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(runtimeDir) {
		bases = append(bases, runtimeDir)
	}
	bases = append(bases, os.TempDir(), "/tmp")

	seen := make(map[string]bool)
	var candidateErrs []error
	for _, base := range bases {
		base = filepath.Clean(base)
		if base == "." || seen[base] {
			continue
		}
		seen[base] = true

		path, err := socketPathInBase(base, root, agentID)
		if err != nil {
			candidateErrs = append(candidateErrs, err)
			continue
		}
		return path, nil
	}
	return "", fmt.Errorf("no usable unix socket directory: %w", errors.Join(candidateErrs...))
}

func socketPathInBase(base, root, agentID string) (string, error) {
	dir := filepath.Join(base, "zut-subagents-"+rootTag(root))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("socket dir %s: %w", dir, err)
	}

	candidate := filepath.Join(dir, agentID+".sock")
	if len(candidate) > maxUnixSocketPath {
		candidate = filepath.Join(dir, shortHash(agentID)+".sock")
	}
	if len(candidate) > maxUnixSocketPath {
		return "", fmt.Errorf("unix socket path too long (%s, %d > %d)", candidate, len(candidate), maxUnixSocketPath)
	}
	if err := probeUnixSocket(dir); err != nil {
		return "", fmt.Errorf("unix sockets unavailable in %s: %w", dir, err)
	}
	return candidate, nil
}

func probeUnixSocket(dir string) error {
	probe, err := os.CreateTemp(dir, ".socket-probe-")
	if err != nil {
		return err
	}
	path := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	closeErr := ln.Close()
	removeErr := os.Remove(path)
	if os.IsNotExist(removeErr) {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
}

// rootTag returns a stable 8-hex-char tag for the subagent root. Used
// in the runtime-directory name so two parallel zut instances with
// different roots don't share sockets.
func rootTag(root string) string { return shortHash(root) }

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:4])
}
