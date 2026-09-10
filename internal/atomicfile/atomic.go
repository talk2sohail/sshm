// Package atomicfile provides crash-safe file replacement: write to a
// temporary file in the destination directory, fsync, then rename.
package atomicfile

import (
	"os"
	"path/filepath"
)

// WriteFile writes data to path via a temporary file in the same
// directory followed by a rename, so a crash or a full disk can never leave a
// half-written config or history file behind.
//
// Both the host store and the history store write through this.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if anything below fails before the rename.
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// fsync before rename: without it a power loss can leave an empty file.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = "" // renamed successfully; nothing to clean up

	// Sync the directory so the rename itself is durable.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
