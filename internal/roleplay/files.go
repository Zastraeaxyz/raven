package roleplay

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// writeFileAtomic writes a file through a temporary sibling and renames it into
// place. Files under the roleplay data directory are private.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".roleplay-*")
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("setting file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// pruneBackups keeps the newest limit .md files in dir.
func pruneBackups(dir string, limit int) {
	if limit < 1 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for len(names) > limit {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}
