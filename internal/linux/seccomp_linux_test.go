package linux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func TestSeccomp(t *testing.T) {
	if mode := os.Getenv("MINIRUNC_SECCOMP_CHILD"); mode != "" {
		runtime.LockOSThread()
		testSeccompChild(t, mode)
		return
	}
	for _, mode := range []string{"errno", "zero errno", "masked argument", "and arguments", "thread sync", "kill process", "unknown syscall", "no implicit nnp", "whitelist"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "no implicit nnp" && os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
				t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test installation after identity change")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSeccomp$")
			child.Env = append(os.Environ(), "MINIRUNC_SECCOMP_CHILD="+mode)
			output, err := child.CombinedOutput()
			if mode == "kill process" {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGSYS {
					t.Fatalf("kill process: %v\n%s", err, output)
				}
			} else if err != nil {
				t.Fatalf("seccomp child: %v\n%s", err, output)
			}
		})
	}
}

func testSeccompChild(t *testing.T, mode string) {
	config := &specs.LinuxSeccomp{
		DefaultAction: specs.ActAllow,
		Syscalls:      []specs.LinuxSyscall{{Names: []string{"getppid"}, Action: specs.ActErrno}},
	}
	if mode == "no implicit nnp" {
		before, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if before != 0 {
			t.Fatal("installation-permission test requires inherited no_new_privs=0")
		}
		if err := SetupCredentials(specs.User{UID: 1000, GID: 1000}, nil); err != nil {
			t.Fatal(err)
		}
		if err := SetupSeccomp(config); !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EPERM) && !errors.Is(err, unix.ECANCELED) {
			t.Fatalf("load without CAP_SYS_ADMIN or NNP: %v", err)
		}
		if got, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0); err != nil || got != 0 {
			t.Fatalf("libseccomp implicitly enabled NNP: %d, error %v", got, err)
		}
		return
	}
	if err := SetNoNewPrivileges(true); err != nil {
		t.Fatal(err)
	}
	if mode == "unknown syscall" {
		config.Syscalls[0].Names = []string{"minirunc_not_a_syscall"}
		if err := SetupSeccomp(config); err == nil || !strings.Contains(err.Error(), "resolve seccomp") {
			t.Fatalf("unknown syscall: %v", err)
		}
		return
	}
	if mode == "kill process" {
		config.Syscalls[0].Action = specs.ActKillProcess
	}
	if mode == "zero errno" {
		zero := uint(0)
		config.Syscalls[0].ErrnoRet = &zero
	}
	if mode == "masked argument" || mode == "and arguments" {
		config.Syscalls[0].Names = []string{"getpgid"}
		config.Syscalls[0].Args = []specs.LinuxSeccompArg{{Index: 0, Op: specs.OpMaskedEqual, Value: 0xff, ValueTwo: 0}}
		if mode == "and arguments" {
			config.Syscalls[0].Args = []specs.LinuxSeccompArg{
				{Index: 0, Op: specs.OpEqualTo, Value: 0}, {Index: 1, Op: specs.OpEqualTo, Value: 123},
			}
		}
	}
	if mode == "whitelist" {
		config.DefaultAction = specs.ActErrno
		config.Syscalls = []specs.LinuxSyscall{{Action: specs.ActAllow, Names: []string{
			"write", "read", "close", "exit", "exit_group", "futex", "rt_sigreturn", "rt_sigprocmask",
			"getpid", "gettid", "tgkill", "sched_yield", "mmap", "munmap", "mprotect", "madvise",
			"brk", "nanosleep", "clock_gettime", "sigaltstack", "prctl",
		}}}
	}
	if mode == "thread sync" {
		// 保持另一线程等待；安装后让它主动调用被禁 syscall，证明 TSYNC 实际生效。
		ready, proceed, checked := make(chan struct{}), make(chan struct{}), make(chan unix.Errno)
		go func() {
			runtime.LockOSThread()
			close(ready)
			<-proceed
			_, _, errno := unix.RawSyscall(unix.SYS_GETPPID, 0, 0, 0)
			checked <- errno
		}()
		<-ready
		if err := SetupSeccomp(config); err != nil {
			t.Fatal(err)
		}
		close(proceed)
		if errno := <-checked; errno != unix.EPERM {
			t.Fatalf("other thread was not filtered: %v", errno)
		}
		return
	}
	if err := SetupSeccomp(config); err != nil {
		t.Fatal(err)
	}
	if mode == "masked argument" || mode == "and arguments" {
		if _, _, errno := unix.RawSyscall(unix.SYS_GETPGID, 0, 123, 0); errno != unix.EPERM {
			t.Fatalf("matched argument: %v", errno)
		}
		if mode == "and arguments" {
			if _, _, errno := unix.RawSyscall(unix.SYS_GETPGID, 0, 124, 0); errno != 0 {
				t.Fatalf("unmatched second argument: %v", errno)
			}
		} else if _, _, errno := unix.RawSyscall(unix.SYS_GETPGID, 1, 0, 0); errno == unix.EPERM {
			t.Fatal("mask matched an argument that should pass")
		}
		return
	}
	raw, _, errno := unix.RawSyscall(unix.SYS_GETPPID, 0, 0, 0)
	if mode == "zero errno" {
		if errno != 0 || raw != 0 {
			t.Fatalf("errno action with return value zero: %d, %v", raw, errno)
		}
	} else if errno != unix.EPERM {
		t.Fatalf("getppid was not blocked: %v", errno)
	}
	if _, _, errno := unix.RawSyscall(unix.SYS_GETPID, 0, 0, 0); errno != 0 {
		t.Fatalf("allowed syscall: %v", errno)
	}
}
