package spec

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLoadBundle(t *testing.T) {
	dir := t.TempDir()
	rootfsPath := filepath.Join(dir, "rootfs")
	if err := os.Mkdir(rootfsPath, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"ociVersion":"1.3.0","root":{"path":"rootfs"},"x-test":true}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	bundle, err := LoadBundle(".")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Dir != dir {
		t.Errorf("bundle directory = %q, want %q", bundle.Dir, dir)
	}
	if bundle.Spec.Version != "1.3.0" {
		t.Errorf("OCI version = %q, want 1.3.0", bundle.Spec.Version)
	}
	if bundle.Spec.Root == nil || bundle.Spec.Root.Path != "rootfs" {
		t.Errorf("root = %+v, want path rootfs", bundle.Spec.Root)
	}
	if bundle.RootfsPath != rootfsPath {
		t.Errorf("rootfs path = %q, want %q", bundle.RootfsPath, rootfsPath)
	}
}

func TestLoadBundleWithAbsoluteRootfsPath(t *testing.T) {
	bundleDir := t.TempDir()
	rootfsDir := t.TempDir()
	data := []byte(`{"root":{"path":` + strconv.Quote(rootfsDir) + `}}`)
	if err := os.WriteFile(filepath.Join(bundleDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	bundle, err := LoadBundle(bundleDir)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.RootfsPath != rootfsDir {
		t.Errorf("rootfs path = %q, want %q", bundle.RootfsPath, rootfsDir)
	}
}

func TestLoadBundleMissingConfig(t *testing.T) {
	_, err := LoadBundle(t.TempDir())
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
}

func TestLoadBundleInvalidJSON(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "empty", data: ""},
		{name: "malformed", data: "{"},
		{name: "null", data: "null"},
		{name: "array", data: "[]"},
		{name: "wrong field type", data: `{"ociVersion":1}`},
		{name: "multiple objects", data: "{} {}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tt.data), 0600); err != nil {
				t.Fatal(err)
			}
			bundle, err := LoadBundle(dir)
			if err == nil {
				t.Fatal("expected a JSON decoding error")
			}
			if bundle != nil {
				t.Errorf("bundle = %+v, want nil on error", bundle)
			}
		})
	}
}

func TestLoadBundleInvalidRoot(t *testing.T) {
	tests := []struct {
		name   string
		config string
		setup  func(t *testing.T, dir string)
	}{
		{name: "missing root", config: `{}`},
		{name: "null root", config: `{"root":null}`},
		{name: "empty path", config: `{"root":{"path":""}}`},
		{name: "missing path", config: `{"root":{}}`},
		{name: "path does not exist", config: `{"root":{"path":"missing"}}`},
		{
			name:   "path is a file",
			config: `{"root":{"path":"rootfs"}}`,
			setup: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, "rootfs"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, dir)
			}
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tt.config), 0600); err != nil {
				t.Fatal(err)
			}

			bundle, err := LoadBundle(dir)
			if err == nil {
				t.Fatal("expected a root configuration error")
			}
			if bundle != nil {
				t.Errorf("bundle = %+v, want nil on error", bundle)
			}
		})
	}
}
