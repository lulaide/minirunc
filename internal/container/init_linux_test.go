package container

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lulaide/minirunc/internal/cgroup"
	specs "github.com/opencontainers/runtime-spec/specs-go"
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
		config, err := readInitConfig(os.NewFile(configFD, "init-config"))
		if err != nil {
			t.Fatal(err)
		}
		if config.Spec.Linux.CgroupsPath != "" {
			// 在收到配置、创建 cgroup namespace 之前，确认父进程已经迁移本进程。
			data, err := os.ReadFile("/proc/self/cgroup")
			if err != nil || !strings.HasSuffix(strings.TrimSpace(string(data)), "/"+config.Spec.Linux.CgroupsPath) {
				t.Fatalf("init not joined before receiving configuration: %q, error %v", data, err)
			}
		}
		if err := setupInit(config, os.NewFile(parentMountFD, "parent-mnt"), os.NewFile(parentUTSFD, "parent-uts")); err != nil {
			t.Fatal(err)
		}
		if os.Getpid() != 1 {
			t.Fatalf("container PID = %d, want 1", os.Getpid())
		}
		if config.Spec.Linux.CgroupsPath != "" {
			data, err := os.ReadFile("/proc/self/cgroup")
			if err != nil || strings.TrimSpace(string(data)) != "0::/" {
				t.Fatalf("cgroup namespace root = %q, error %v", data, err)
			}
		}
		ruid, euid, suid := unix.Getresuid()
		rgid, egid, sgid := unix.Getresgid()
		if ruid != 1000 || euid != 1000 || suid != 1000 || rgid != 1000 || egid != 1000 || sgid != 1000 {
			t.Fatalf("UIDs = %d/%d/%d, GIDs = %d/%d/%d", ruid, euid, suid, rgid, egid, sgid)
		}
		if groups, err := os.Getgroups(); err != nil || !reflect.DeepEqual(groups, []int{1001, 1002}) {
			t.Fatalf("supplementary groups = %v, error %v", groups, err)
		}
		if cwd, err := os.Getwd(); err != nil || cwd != "/tmp" {
			t.Fatalf("working directory = %q, error %v", cwd, err)
		}
		var limit unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil || limit.Cur != 64 || limit.Max != 128 {
			t.Fatalf("NOFILE = %+v, error %v", limit, err)
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
		t.Log("Ubuntu init checks passed: PID 1, hostname, rootfs, procfs, readonly and masked paths, user, groups, cwd, rlimits")
		return
	}
	if os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
		t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test init isolation")
	}
	config := ubuntuInitConfig(t)
	config.Spec.Process.User = specs.User{UID: 1000, GID: 1000, AdditionalGids: []uint32{1001, 1002}}
	config.Spec.Process.Cwd = "/tmp"
	config.Spec.Process.Rlimits = []specs.POSIXRlimit{{Type: "RLIMIT_NOFILE", Soft: 64, Hard: 128}}
	var group *cgroup.Group
	if os.Getenv("MINIRUNC_CGROUP_TEST_PARENT") != "" {
		config.CgroupParent = containerTestCgroupParent(t)
		config.Spec.Linux.CgroupsPath = "ubuntu-init"
		config.Spec.Linux.Resources = testCgroupResources()
		var err error
		group, err = cgroup.Create(config.CgroupParent, config.Spec.Linux.CgroupsPath, config.Spec.Linux.Resources)
		if err != nil {
			t.Fatal(err)
		}
		defer group.Close()
		defer func() {
			if err := group.Remove(); err != nil {
				t.Errorf("remove init test cgroup: %v", err)
			}
		}()
	}
	flags, err := initCloneFlags(config)
	if err != nil {
		t.Fatal(err)
	}
	placeholder, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer placeholder.Close()
	configRead, configWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer configRead.Close()
	defer configWrite.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSetupInitWithUbuntuBundle$", "-test.v")
	child.Env = append(os.Environ(), "MINIRUNC_INIT_TEST_CHILD=1")
	child.SysProcAttr = &syscall.SysProcAttr{Cloneflags: flags}
	// fd 3 与真实入口一样等待配置；结果由测试输出报告，fd 4 仅占位。
	child.ExtraFiles = []*os.File{configRead, placeholder, openTestNamespace(t, "mnt"), openTestNamespace(t, "uts")}
	var output strings.Builder
	child.Stdout, child.Stderr = &output, &output
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
	if group != nil {
		if err := group.AddPID(child.Process.Pid); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeInitMessage(configWrite, config); err != nil {
		t.Fatal(err)
	}
	_ = configWrite.Close()
	err = child.Wait()
	waited = true
	if err != nil {
		t.Fatalf("check Ubuntu init: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "Ubuntu init checks passed") {
		t.Fatalf("missing init checks: %s", output.String())
	}
	t.Log(output.String())
}
