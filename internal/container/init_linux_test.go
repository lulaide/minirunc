package container

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

	"golang.org/x/sys/unix"
)

func TestInitRejectsSharedNamespaces(t *testing.T) {
	executable := buildInitExecutable(t)
	configRead, configWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer configRead.Close()
	defer configWrite.Close()
	resultRead, resultWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer resultRead.Close()
	defer resultWrite.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, initCommand)
	child.Env = []string{}
	child.ExtraFiles = []*os.File{configRead, resultWrite, openTestNamespace(t, "mnt"), openTestNamespace(t, "uts")}
	// 故意不设置 Cloneflags：配置声明隔离，实际却仍在父进程的 namespace 中。
	// 真正的 __init 必须在调用 mount 或 Sethostname 之前拒绝它。
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	_ = configRead.Close()
	_ = resultWrite.Close()
	if err := writeInitMessage(configWrite, testInitConfig(t)); err != nil {
		t.Fatal(err)
	}
	_ = configWrite.Close()
	result, err := readInitResult(resultRead)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || !strings.Contains(result.Error, "must be in a new mnt namespace") {
		t.Fatalf("shared namespaces were not rejected: %+v", result)
	}
	var exitErr *exec.ExitError
	err = child.Wait()
	waited = true
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("child exit = %v, want exit code 1", err)
	}
}

func TestRequireNewNamespace(t *testing.T) {
	for _, test := range []struct {
		name string
		kind int
	}{
		{"mnt", unix.CLONE_NEWNS},
		{"uts", unix.CLONE_NEWUTS},
	} {
		if err := requireNewNamespace(test.name, test.kind, openTestNamespace(t, test.name)); err == nil {
			t.Fatalf("shared %s namespace was accepted", test.name)
		}
	}
	if err := requireNewNamespace("mnt", unix.CLONE_NEWNS, openTestNamespace(t, "uts")); err == nil ||
		!strings.Contains(err.Error(), "unexpected namespace type") {
		t.Fatalf("wrong namespace type: %v", err)
	}
	notNamespace, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer notNamespace.Close()
	if err := requireNewNamespace("mnt", unix.CLONE_NEWNS, notNamespace); err == nil {
		t.Fatal("ordinary file was accepted as a namespace reference")
	}
}

func TestSetupInitWithUbuntuBundle(t *testing.T) {
	if os.Getenv("MINIRUNC_INIT_TEST_CHILD") == "1" {
		runtime.LockOSThread()
		config := ubuntuInitConfig(t)
		if err := setupInit(config, os.NewFile(parentMountFD, "parent-mnt"), os.NewFile(parentUTSFD, "parent-uts")); err != nil {
			t.Fatal(err)
		}
		if os.Getpid() != 1 {
			t.Fatalf("container PID = %d, want 1", os.Getpid())
		}
		if name, err := os.Hostname(); err != nil || name != config.Spec.Hostname {
			t.Fatalf("container hostname = %q, error %v", name, err)
		}
		data, err := os.ReadFile("/etc/os-release")
		if err != nil || !strings.Contains(string(data), "Ubuntu 24.04") {
			t.Fatalf("container rootfs is not Ubuntu: %v", err)
		}
		var fs unix.Statfs_t
		if err := unix.Statfs("/proc", &fs); err != nil || fs.Type != unix.PROC_SUPER_MAGIC {
			t.Fatalf("/proc is not procfs: %v", err)
		}
		for _, path := range []string{"/", "/proc/sys"} {
			if err := unix.Statfs(path, &fs); err != nil || fs.Flags&unix.ST_RDONLY == 0 {
				t.Fatalf("%s is not readonly: %v", path, err)
			}
		}
		var device unix.Stat_t
		if err := unix.Stat("/proc/kcore", &device); err != nil || device.Mode&unix.S_IFMT != unix.S_IFCHR ||
			unix.Major(uint64(device.Rdev)) != 1 || unix.Minor(uint64(device.Rdev)) != 3 {
			t.Fatalf("/proc/kcore is not masked by /dev/null: %v", err)
		}
		if _, err := os.Stat("/.minirunc-oldroot"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("old root is still reachable")
		}
		t.Log("Ubuntu init checks passed: PID 1, hostname, rootfs, procfs, readonly and masked paths")
		return
	}
	if os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
		t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test init isolation")
	}
	config := ubuntuInitConfig(t)
	flags, err := initCloneFlags(config)
	if err != nil {
		t.Fatal(err)
	}
	placeholder, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer placeholder.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSetupInitWithUbuntuBundle$", "-test.v")
	child.Env = append(os.Environ(), "MINIRUNC_INIT_TEST_CHILD=1")
	child.SysProcAttr = &syscall.SysProcAttr{Cloneflags: flags}
	// 此子进程直接检查初始化后的环境；fd 3、fd 4 仅占位，fd 5、fd 6
	// 与真实 __init 的 namespace 引用保持相同布局。
	child.ExtraFiles = []*os.File{placeholder, placeholder, openTestNamespace(t, "mnt"), openTestNamespace(t, "uts")}
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("check Ubuntu init: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Ubuntu init checks passed") {
		t.Fatalf("missing init checks: %s", output)
	}
	t.Logf("%s", output)
}
