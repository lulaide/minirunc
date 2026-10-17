package container

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestStartInitWithSeccomp(t *testing.T) {
	parent := containerTestCgroupParent(t)
	executable := buildInitExecutable(t)
	for _, test := range []struct {
		name   string
		change func(*initConfig)
		want   string
		signal syscall.Signal
	}{
		{"successful", func(*initConfig) {}, "", 0},
		{"unknown-syscall", func(c *initConfig) {
			c.Spec.Linux.Seccomp.Syscalls[0].Names = []string{"minirunc_not_a_syscall"}
		}, "resolve seccomp", 0},
		{"load-denied", func(c *initConfig) {
			c.Spec.Process.NoNewPrivileges = false
		}, "load seccomp filter", 0},
		{"blocked-result", func(c *initConfig) {
			c.Spec.Linux.Seccomp.Syscalls[0].Names = []string{"write"}
		}, "seccomp stage did not confirm readiness", 0},
		{"killed-result", func(c *initConfig) {
			c.Spec.Linux.Seccomp.Syscalls[0] = specs.LinuxSyscall{Names: []string{"write"}, Action: specs.ActKillProcess}
		}, "seccomp stage did not confirm readiness", syscall.SIGSYS},
		{"default-kill", func(c *initConfig) {
			c.Spec.Linux.Seccomp = &specs.LinuxSeccomp{DefaultAction: specs.ActKillProcess}
		}, "seccomp stage did not confirm readiness", syscall.SIGSYS},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := ubuntuInitConfig(t)
			config.CgroupParent = parent
			config.Spec.Linux.CgroupsPath = test.name
			config.Spec.Linux.Resources = testCgroupResources()
			config.Spec.Process.User = specs.User{UID: 1000, GID: 1000}
			config.Spec.Process.Cwd = "/tmp"
			config.Spec.Linux.Seccomp = &specs.LinuxSeccomp{
				DefaultAction: specs.ActAllow,
				Syscalls:      []specs.LinuxSyscall{{Names: []string{"getppid"}, Action: specs.ActErrno}},
			}
			test.change(config)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := startInit(ctx, executable, config)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if test.signal != 0 {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != test.signal {
					t.Fatalf("error = %v, want signal %v", err, test.signal)
				}
			}
			if ctx.Err() != nil {
				t.Fatalf("initialization did not terminate promptly: %v", ctx.Err())
			}
			if _, err := os.Stat(filepath.Join(parent, test.name)); !os.IsNotExist(err) {
				t.Fatalf("cgroup remained after seccomp initialization: %v", err)
			}
		})
	}
}
