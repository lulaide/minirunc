package spec

import (
	"strings"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func baselineSpec() *specs.Spec {
	return &specs.Spec{
		Version: OCIVersion,
		Root:    &specs.Root{Path: "rootfs"},
		Process: &specs.Process{Args: []string{"sh", "-c", ""}, Cwd: "/", Env: []string{"PATH=/bin", "EMPTY=", "VALUE=a=b"}},
	}
}

func TestValidateSpec(t *testing.T) {
	tests := []struct {
		name   string
		change func(*specs.Spec)
		field  string
	}{
		{"version", func(s *specs.Spec) { s.Version = "1.2.0" }, "ociVersion"},
		{"platform", func(s *specs.Spec) { s.Windows = &specs.Windows{} }, "non-Linux"},
		{"root", func(s *specs.Spec) { s.Root = nil }, "root.path"},
		{"process", func(s *specs.Spec) { s.Process = nil }, "process:"},
		{"args", func(s *specs.Spec) { s.Process.Args = nil }, "process.args"},
		{"empty executable", func(s *specs.Spec) { s.Process.Args[0] = "" }, "process.args"},
		{"argument NUL", func(s *specs.Spec) { s.Process.Args = []string{"sh", "a\x00b"} }, "process.args[1]"},
		{"cwd", func(s *specs.Spec) { s.Process.Cwd = "work" }, "process.cwd"},
		{"environment", func(s *specs.Spec) { s.Process.Env = []string{"TOKEN"} }, "process.env[0]"},
		{"empty env name", func(s *specs.Spec) { s.Process.Env = []string{"=secret"} }, "process.env[0]"},
		{"environment NUL", func(s *specs.Spec) { s.Process.Env = []string{"TOKEN=secret\x00"} }, "process.env[0]"},
		{"mount", func(s *specs.Spec) { s.Mounts = []specs.Mount{{Destination: "proc"}} }, "mounts[0].destination"},
		{"namespace type", func(s *specs.Spec) { s.Linux = &specs.Linux{Namespaces: []specs.LinuxNamespace{{Type: "unknown"}}} }, "linux.namespaces[0].type"},
		{"duplicate namespace", func(s *specs.Spec) {
			s.Linux = &specs.Linux{Namespaces: []specs.LinuxNamespace{{Type: specs.PIDNamespace}, {Type: specs.PIDNamespace}}}
		}, "duplicate"},
		{"namespace path", func(s *specs.Spec) {
			s.Linux = &specs.Linux{Namespaces: []specs.LinuxNamespace{{Type: specs.PIDNamespace, Path: "ns/pid"}}}
		}, "linux.namespaces[0].path"},
		{"masked path", func(s *specs.Spec) { s.Linux = &specs.Linux{MaskedPaths: []string{"proc/keys"}} }, "linux.maskedPaths[0]"},
		{"readonly path", func(s *specs.Spec) { s.Linux = &specs.Linux{ReadonlyPaths: []string{"/proc\x00"}} }, "linux.readonlyPaths[0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := baselineSpec()
			tt.change(config)
			err := ValidateSpec(config)
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("error = %v, want field %q", err, tt.field)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("validation error exposes an environment value")
			}
		})
	}
}

func TestValidateSpecAcceptsBaseline(t *testing.T) {
	config := baselineSpec()
	config.Mounts = []specs.Mount{{Destination: "/proc", Type: "proc", Source: "proc"}}
	config.Linux = &specs.Linux{Namespaces: []specs.LinuxNamespace{
		{Type: specs.MountNamespace}, {Type: specs.PIDNamespace, Path: "/proc/1/ns/pid"},
	}}
	if err := ValidateSpec(config); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSpecReportsMultipleProblems(t *testing.T) {
	err := ValidateSpec(&specs.Spec{})
	for _, field := range []string{"ociVersion", "root.path", "process:"} {
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Fatalf("error = %v, want field %q", err, field)
		}
	}
	if ValidateSpec(nil) == nil {
		t.Fatal("nil configuration must fail validation")
	}
}
