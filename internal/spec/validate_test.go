package spec

import (
	"math"
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
		{"reserved UID", func(s *specs.Spec) { s.Process.User.UID = math.MaxUint32 }, "process.user.uid"},
		{"reserved GID", func(s *specs.Spec) { s.Process.User.GID = math.MaxUint32 }, "process.user.gid"},
		{"reserved additional GID", func(s *specs.Spec) { s.Process.User.AdditionalGids = []uint32{1000, math.MaxUint32} }, "process.user.additionalGids[1]"},
		{"empty rlimit type", func(s *specs.Spec) { s.Process.Rlimits = []specs.POSIXRlimit{{}} }, "process.rlimits[0].type"},
		{"unknown rlimit type", func(s *specs.Spec) { s.Process.Rlimits = []specs.POSIXRlimit{{Type: "RLIMIT_UNKNOWN"}} }, "process.rlimits[0].type"},
		{"duplicate rlimit", func(s *specs.Spec) {
			s.Process.Rlimits = []specs.POSIXRlimit{{Type: "RLIMIT_NOFILE"}, {Type: "RLIMIT_NOFILE"}}
		}, "process.rlimits[1].type: duplicate"},
		{"rlimit soft exceeds hard", func(s *specs.Spec) {
			s.Process.Rlimits = []specs.POSIXRlimit{{Type: "RLIMIT_NOFILE", Soft: 1025, Hard: 1024}}
		}, "process.rlimits[0].soft"},
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

func TestValidateSpecAcceptsProcessAttributes(t *testing.T) {
	config := baselineSpec()
	config.Process.User = specs.User{UID: math.MaxUint32 - 1, GID: 1000, AdditionalGids: []uint32{0, 1001, math.MaxUint32 - 1}}
	config.Process.Cwd = "/work"
	// 这里只验证配置，不查宿主机的用户数据库、目录或当前资源上限。
	for _, resource := range []string{
		"RLIMIT_AS", "RLIMIT_CORE", "RLIMIT_CPU", "RLIMIT_DATA",
		"RLIMIT_FSIZE", "RLIMIT_LOCKS", "RLIMIT_MEMLOCK", "RLIMIT_MSGQUEUE",
		"RLIMIT_NICE", "RLIMIT_NOFILE", "RLIMIT_NPROC", "RLIMIT_RSS",
		"RLIMIT_RTPRIO", "RLIMIT_RTTIME", "RLIMIT_SIGPENDING", "RLIMIT_STACK",
	} {
		config.Process.Rlimits = append(config.Process.Rlimits, specs.POSIXRlimit{Type: resource, Soft: math.MaxUint64, Hard: math.MaxUint64})
	}
	if err := ValidateSpec(config); err != nil {
		t.Fatal(err)
	}
	config.Process.Rlimits = []specs.POSIXRlimit{{Type: "RLIMIT_CORE", Soft: 0, Hard: 0}, {Type: "RLIMIT_NOFILE", Soft: 1024, Hard: 4096}}
	if err := ValidateSpec(config); err != nil {
		t.Fatal(err)
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
