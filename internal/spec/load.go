package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type Bundle struct {
	Dir  string
	Spec *specs.Spec
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

	return &Bundle{Dir: absDir, Spec: config}, nil
}
