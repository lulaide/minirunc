package command

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCommand(t *testing.T) {
	dir := testBundle(t)
	configPath := filepath.Join(dir, "config.json")
	valid := `{"ociVersion":"1.3.0","root":{"path":"rootfs"},"process":{"user":{"uid":0,"gid":0},"args":["/bin/sh"],"cwd":"/"}}`
	tests := []struct {
		name   string
		config string
		code   int
		output string
	}{
		{"valid bundle", valid, 0, "baseline configuration checks passed"},
		{"invalid process", `{"ociVersion":"1.3.0","root":{"path":"rootfs"},"process":{"cwd":"relative"}}`, 1, "process.cwd"},
		{"missing rootfs", `{"root":{"path":"missing"}}`, 1, "root.path"},
		{"invalid JSON", `{`, 1, "decode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(configPath, []byte(tt.config), 0600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := Execute(context.Background(), []string{"validate", "--bundle", dir}, &stdout, &stderr)
			if code != tt.code {
				t.Fatalf("exit code = %d, want %d; stderr = %s", code, tt.code, &stderr)
			}
			if !strings.Contains(stdout.String()+stderr.String(), tt.output) {
				t.Fatalf("stdout = %q, stderr = %q, want %q", &stdout, &stderr, tt.output)
			}
			if code != 0 && stdout.Len() != 0 {
				t.Fatalf("failure wrote to stdout: %s", &stdout)
			}
			if code == 0 && stderr.Len() != 0 {
				t.Fatalf("success wrote diagnostics without --debug: %s", &stderr)
			}
		})
	}
}

func TestCommandUsage(t *testing.T) {
	for _, args := range [][]string{{"run"}, {"validate", "extra"}, {"validate", "--unknown"}, {"--log-format", "yaml", "validate"}} {
		var stdout, stderr bytes.Buffer
		if code := Execute(context.Background(), args, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "Usage:") {
			t.Fatalf("args = %v, exit code = %d, stderr = %q", args, code, &stderr)
		}
	}
	for _, args := range [][]string{nil, {"--help"}, {"validate", "--help"}} {
		var stdout, stderr bytes.Buffer
		if code := Execute(context.Background(), args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "Usage:") || stderr.Len() != 0 {
			t.Fatalf("args = %v, exit code = %d, stdout = %q, stderr = %q", args, code, &stdout, &stderr)
		}
		if len(args) <= 1 && !strings.Contains(stdout.String(), "built for the OCI Runtime Specification") {
			t.Fatalf("root help does not contain the minirunc description: %q", &stdout)
		}
		if len(args) <= 1 && strings.Contains(stdout.String(), "completion") {
			t.Fatalf("root help contains Cobra's default completion command: %q", &stdout)
		}
	}
}

func TestJSONLog(t *testing.T) {
	dir := testBundle(t)
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"--log-format", "json", "validate", "--bundle", dir}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %s", code, &stderr)
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &entry); err != nil {
		t.Fatalf("decode JSON log %q: %v", &stderr, err)
	}
	for key, want := range map[string]string{"level": "error", "message": "command failed", "command": "minirunc validate", "operation": "load bundle"} {
		if entry[key] != want {
			t.Errorf("%s = %v, want %q", key, entry[key], want)
		}
	}
}

func TestDebugAndLogFile(t *testing.T) {
	dir := testBundle(t)
	valid := `{"ociVersion":"1.3.0","root":{"path":"rootfs"},"process":{"user":{"uid":0,"gid":0},"args":["/bin/sh"],"cwd":"/"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "minirunc.log")
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"--debug", "--log", logPath, "validate", "-b", dir}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, &stderr)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "debug") || !strings.Contains(string(data), "bundle validation passed") {
		t.Fatalf("log = %q, want debug validation entry", data)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("log mode = %v, want 0600", info.Mode().Perm())
	}
}

func testBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "rootfs"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}
