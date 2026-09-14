package spec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBundle(t *testing.T) {
	dir := t.TempDir()
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
