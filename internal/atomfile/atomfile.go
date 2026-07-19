// Package atomfile reads and writes small secret-bearing JSON files (token
// and session stores) with 0600 permissions and atomic replacement — the temp
// file is created in the destination directory and renamed over the target —
// so a crash mid-write can never leave a truncated or world-readable store.
package atomfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReadJSON decodes the JSON file at path into v. It reports found=false with
// a nil error when the file does not exist, so callers can treat a missing
// store as "never bootstrapped" rather than a failure.
func ReadJSON(path string, v any) (found bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("parsing %s: %w", path, err)
	}
	return true, nil
}

// WriteJSON writes v as indented JSON to path with 0600 permissions,
// atomically: temp file in the same directory, chmod before the content is
// written, then rename over path. The parent directory must already exist.
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
