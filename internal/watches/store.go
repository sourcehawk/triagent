package watches

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type fileShape struct {
	Watches []Watch `yaml:"watches"`
}

// LoadWatches reads user_watches.yaml. Missing file → empty slice.
// Bad yaml → error. Per-watch Validate is NOT called here; loaders
// stay permissive so launcher boot survives historical entries.
func LoadWatches(path string) ([]Watch, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(body) == 0 {
		return nil, nil
	}
	var f fileShape
	if err := yaml.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return f.Watches, nil
}

// SaveWatches writes user_watches.yaml atomically with mode 0600.
// All entries are validated; first error aborts the write.
func SaveWatches(path string, ws []Watch) error {
	for i, w := range ws {
		if err := w.Validate(); err != nil {
			return fmt.Errorf("watch[%d] %s: %w", i, w.ID, err)
		}
	}
	body, err := yaml.Marshal(fileShape{Watches: ws})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DefaultUserWatchesPath returns $XDG_CONFIG_HOME/c1/investigate/user_watches.yaml.
// Mirrors repos.DefaultConfigDir's resolution.
func DefaultUserWatchesPath() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "triagent", "user_watches.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "triagent", "user_watches.yaml"), nil
}

// WatchDir returns the per-watch data directory beside user_watches.yaml.
func WatchDir(userWatchesPath, watchID string) string {
	root := filepath.Dir(userWatchesPath)
	return filepath.Join(root, "watches", watchID)
}
