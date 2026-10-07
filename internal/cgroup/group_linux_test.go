package cgroup

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func TestCreateRejectsInvalidName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../other", "/absolute", "nested/group", "system.slice:runtime:id", "name\x00"} {
		if _, err := Create(t.TempDir(), name, nil); err == nil || !strings.Contains(err.Error(), "cgroupsPath") {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	if _, err := Create(t.TempDir(), "container", nil); err == nil || !strings.Contains(err.Error(), "cgroup v2 filesystem") {
		t.Fatalf("ordinary parent directory: %v", err)
	}
}

func TestGroupLifecycle(t *testing.T) {
	if os.Getenv("MINIRUNC_CGROUP_CHILD") == "1" {
		// 子进程等待父进程关闭 stdin，期间可检查迁移和“有任务不能删除”的约束。
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			t.Fatal(err)
		}
		return
	}
	parent := ownedTestParent(t)
	resources := &specs.LinuxResources{
		Memory: &specs.LinuxMemory{Limit: pointer(int64(67108864))},
		CPU:    &specs.LinuxCPU{Quota: pointer(int64(50000))},
		Pids:   &specs.LinuxPids{Limit: pointer(int64(32))},
	}
	group, err := Create(parent, "container", resources)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	defer func() {
		if err := group.Remove(); err != nil {
			t.Errorf("remove test group: %v", err)
		}
	}()
	if _, err := Create(parent, "container", nil); !errors.Is(err, unix.EEXIST) {
		t.Fatalf("existing group must not be reused: %v", err)
	}
	if err := group.AddPID(0); err == nil {
		t.Fatal("accepted PID 0")
	}
	if err := group.AddPID(1 << 30); !errors.Is(err, unix.ESRCH) {
		t.Fatalf("nonexistent PID: %v", err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestGroupLifecycle$")
	child.Env = append(os.Environ(), "MINIRUNC_CGROUP_CHILD=1")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		_ = input.Close()
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	if err := group.AddPID(child.Process.Pid); err != nil {
		t.Fatal(err)
	}
	procs, err := readControl(group.directory, "cgroup.procs")
	if err != nil || !containsWord(procs, strconv.Itoa(child.Process.Pid)) {
		t.Fatalf("child not in cgroup.procs: %q, error %v", procs, err)
	}
	for file, want := range map[string]string{"memory.max": "67108864", "cpu.max": "50000 100000", "pids.max": "32"} {
		got, err := readControl(group.directory, file)
		if err != nil || strings.TrimSpace(got) != want {
			t.Fatalf("%s = %q, error %v", file, got, err)
		}
	}
	if err := group.Remove(); !errors.Is(err, unix.EBUSY) {
		t.Fatalf("remove occupied group: %v", err)
	}
	_ = input.Close()
	err = child.Wait()
	waited = true
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateCleansUpFailedLimits(t *testing.T) {
	parent := ownedTestParent(t)
	// 参数格式合法，但内核拒绝小于最小周期的 CPU period。
	_, err := Create(parent, "failed", &specs.LinuxResources{CPU: &specs.LinuxCPU{Period: pointer(uint64(1))}})
	if err == nil || !strings.Contains(err.Error(), "cpu.max") {
		t.Fatalf("kernel-invalid period: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "failed")); !os.IsNotExist(err) {
		t.Fatalf("failed group was not removed: %v", err)
	}
}

func ownedTestParent(t *testing.T) string {
	t.Helper()
	if os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
		t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test cgroup lifecycle")
	}
	root := os.Getenv("MINIRUNC_CGROUP_TEST_PARENT")
	if root == "" {
		t.Fatal("MINIRUNC_CGROUP_TEST_PARENT must name a writable delegated parent")
	}
	parent, err := os.MkdirTemp(root, "minirunc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(parent); err != nil {
			t.Errorf("remove test parent: %v", err)
		}
	})
	return parent
}
