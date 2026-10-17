package spec

import (
	"runtime"
	"strings"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func seccompConfig() *specs.LinuxSeccomp {
	return &specs.LinuxSeccomp{
		DefaultAction: specs.ActAllow,
		Syscalls:      []specs.LinuxSyscall{{Names: []string{"getppid"}, Action: specs.ActErrno}},
	}
}

func TestValidateSeccomp(t *testing.T) {
	if err := ValidateSeccomp(nil); err != nil {
		t.Fatal(err)
	}
	config := seccompConfig()
	config.Architectures = []specs.Arch{map[string]specs.Arch{"amd64": specs.ArchX86_64, "arm64": specs.ArchAARCH64}[runtime.GOARCH]}
	for _, op := range []specs.LinuxSeccompOperator{
		specs.OpNotEqual, specs.OpLessThan, specs.OpLessEqual, specs.OpEqualTo,
		specs.OpGreaterEqual, specs.OpGreaterThan, specs.OpMaskedEqual,
	} {
		config.Syscalls[0].Args = []specs.LinuxSeccompArg{{Index: 5, Value: 3, Op: op}}
		if err := ValidateSeccomp(config); err != nil {
			t.Fatalf("operator %q: %v", op, err)
		}
	}
	for _, errno := range []uint{0, 4095} {
		config.Syscalls[0].ErrnoRet = &errno
		if err := ValidateSeccomp(config); err != nil {
			t.Fatal(err)
		}
	}
	config.Syscalls[0].ErrnoRet = nil
	config.DefaultAction = specs.ActKillProcess
	if err := ValidateSeccomp(config); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSpecRejectsInvalidSeccomp(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*specs.LinuxSeccomp)
		field  string
	}{
		{"default action", func(c *specs.LinuxSeccomp) { c.DefaultAction = "unknown" }, "defaultAction"},
		{"thread kill", func(c *specs.LinuxSeccomp) { c.DefaultAction = specs.ActKillThread }, "defaultAction"},
		{"notify", func(c *specs.LinuxSeccomp) { c.Syscalls[0].Action = specs.ActNotify }, "syscalls[0].action"},
		{"listener", func(c *specs.LinuxSeccomp) { c.ListenerPath = "/listener" }, "notification listeners"},
		{"metadata", func(c *specs.LinuxSeccomp) { c.ListenerMetadata = "metadata" }, "notification listeners"},
		{"flags", func(c *specs.LinuxSeccomp) { c.Flags = []specs.LinuxSeccompFlag{specs.LinuxSeccompFlagLog} }, "flags"},
		{"architecture", func(c *specs.LinuxSeccomp) { c.Architectures = []specs.Arch{specs.ArchX32} }, "architectures[0]"},
		{"names", func(c *specs.LinuxSeccomp) { c.Syscalls[0].Names = nil }, "syscalls[0].names"},
		{"empty name", func(c *specs.LinuxSeccomp) { c.Syscalls[0].Names = []string{""} }, "names[0]"},
		{"name NUL", func(c *specs.LinuxSeccomp) { c.Syscalls[0].Names = []string{"getpid\x00"} }, "names[0]"},
		{"duplicate name", func(c *specs.LinuxSeccomp) { c.Syscalls[0].Names = []string{"getpid", "getpid"} }, "names[1]"},
		{"errno action", func(c *specs.LinuxSeccomp) { n := uint(1); c.DefaultErrnoRet = &n }, "defaultAction"},
		{"errno range", func(c *specs.LinuxSeccomp) { n := uint(4096); c.Syscalls[0].ErrnoRet = &n }, "syscalls[0].action"},
		{"argument index", func(c *specs.LinuxSeccomp) {
			c.Syscalls[0].Args = []specs.LinuxSeccompArg{{Index: 6, Op: specs.OpEqualTo}}
		}, "args[0].index"},
		{"operator", func(c *specs.LinuxSeccomp) { c.Syscalls[0].Args = []specs.LinuxSeccompArg{{Op: "unknown"}} }, "args[0].op"},
		{"valueTwo", func(c *specs.LinuxSeccomp) {
			c.Syscalls[0].Args = []specs.LinuxSeccompArg{{Op: specs.OpEqualTo, ValueTwo: 1}}
		}, "args[0].valueTwo"},
		{"repeated argument", func(c *specs.LinuxSeccomp) {
			c.Syscalls[0].Args = []specs.LinuxSeccompArg{{Op: specs.OpEqualTo}, {Op: specs.OpNotEqual}}
		}, "args[1].index"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := baselineSpec()
			config.Linux = &specs.Linux{Seccomp: seccompConfig()}
			test.change(config.Linux.Seccomp)
			if err := ValidateSpec(config); err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("error = %v, want %q", err, test.field)
			}
		})
	}
}
