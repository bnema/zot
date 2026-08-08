//go:build windows

package subagents

// Windows does not support syncing a directory handle through os.File.Sync.
// The metadata file itself is synced before the atomic rename.
func syncDirectory(string) error {
	return nil
}
