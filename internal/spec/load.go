package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type Bundle struct {
	Dir        string
	RootfsPath string
	Spec       *specs.Spec
}

// LoadBundle reads and decodes the configuration in a bundle directory.
func LoadBundle(dir string) (*Bundle, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve bundle directory %q: %w", dir, err)
	}

	configPath := filepath.Join(absDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read bundle config: %w", err)
	}

	var config *specs.Spec
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode %q: %w", configPath, err)
	}
	if config == nil {
		return nil, fmt.Errorf("decode %q: expected a JSON object", configPath)
	}

	rootfsPath, err := resolveRootfs(absDir, config.Root)
	if err != nil {
		return nil, err
	}

	return &Bundle{Dir: absDir, RootfsPath: rootfsPath, Spec: config}, nil
}

func resolveRootfs(bundleDir string, root *specs.Root) (string, error) {
	if root == nil {
		return "", fmt.Errorf("root: field is required")
	}
	if root.Path == "" {
		return "", fmt.Errorf("root.path: field is required")
	}

	rootfsPath := root.Path
	if !filepath.IsAbs(rootfsPath) {
		rootfsPath = filepath.Join(bundleDir, rootfsPath)
	} else {
		rootfsPath = filepath.Clean(rootfsPath)
	}

	info, err := os.Stat(rootfsPath)
	if err != nil {
		return "", fmt.Errorf("root.path %q: %w", root.Path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root.path %q: not a directory", root.Path)
	}

	return rootfsPath, nil
}
