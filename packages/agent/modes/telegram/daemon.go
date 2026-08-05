package telegram

import (
	"path/filepath"
	"time"

	"github.com/bnema/zut/packages/agent/modes/bot"
)

// PIDPath returns the location of the bot's pid file.
func PIDPath(zutHome string) string { return filepath.Join(zutHome, "bot.pid") }

// LogPath returns the location of the bot's log file (stdout+stderr
// from a detached `zut bot start`).
func LogPath(zutHome string) string { return filepath.Join(zutHome, "logs", "bot.log") }

// WritePID persists pid to bot.pid. Overwrites any existing file.
func WritePID(zutHome string, pid int) error { return bot.WritePIDFile(PIDPath(zutHome), pid) }

// ReadPID returns the pid stored in bot.pid, or 0 if the file doesn't
// exist. Returns an error for any other read/parse failure.
func ReadPID(zutHome string) (int, error) { return bot.ReadPIDFile(PIDPath(zutHome)) }

// RemovePID deletes the pid file if it exists.
func RemovePID(zutHome string) error { return bot.RemovePIDFile(PIDPath(zutHome)) }

// IsRunning returns (pid, true) if a live process with the recorded
// pid exists, or (pid, false) if the pid file points to a dead process.
// Stale pid files are left in place; the caller may remove them.
func IsRunning(zutHome string) (int, bool, error) { return bot.IsRunningAt(PIDPath(zutHome)) }

// StopProcess asks pid to exit and waits up to graceful for it to stop,
// then escalates to a forced kill. Returns nil if the process is gone.
func StopProcess(pid int, graceful time.Duration) error { return bot.StopProcess(pid, graceful) }
