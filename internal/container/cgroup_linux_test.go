package container

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lulaide/minirunc/internal/cgroup"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func testCgroupResources() *specs.LinuxResources {
	memory, quota, pids := int64(134217728), int64(50000), int64(64)
	return &specs.LinuxResources{
		Memory: &specs.LinuxMemory{Limit: &memory},
		CPU:    &specs.LinuxCPU{Quota: &quota},
		Pids:   &specs.LinuxPids{Limit: &pids},
	}
}

func containerTestCgroupParent(t *testing.T) string {
	t.Helper()
	if os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
		t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test init cgroups")
	}
	root := os.Getenv("MINIRUNC_CGROUP_TEST_PARENT")
	if root == "" {
		t.Fatal("MINIRUNC_CGROUP_TEST_PARENT must name a writable delegated parent")
	}
	parent, err := os.MkdirTemp(root, "minirunc-init-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(parent); err != nil {
			t.Errorf("remove init cgroup parent: %v", err)
		}
	})
	return parent
}

func TestStartInitWithCgroup(t *testing.T) {
	parent := containerTestCgroupParent(t)
	executable := buildInitExecutable(t)
	// 这个程序不读取配置，用于验证阻塞等待结果时取消也能回收进程和 cgroup。
	sleeper := filepath.Join(t.TempDir(), "sleep-init")
	if err := os.WriteFile(sleeper, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		executable string
		change     func(*initConfig)
		want       string
		timeout    time.Duration
	}{
		{"successful", executable, func(*initConfig) {}, "", 10 * time.Second},
		{"nonroot", executable, func(c *initConfig) {
			c.Spec.Process.User = specs.User{UID: 1000, GID: 1000}
			c.Spec.Process.Cwd = "/tmp"
		}, "", 10 * time.Second},
		{"init-failure", executable, func(c *initConfig) {
			c.Spec.Process.Cwd = "/minirunc-no-such-directory"
		}, "change working directory", 10 * time.Second},
		{"start-failure", "/minirunc-no-such-executable", func(*initConfig) {}, "start init process", 10 * time.Second},
		{"limit-failure", executable, func(c *initConfig) {
			period := uint64(1)
			c.Spec.Linux.Resources.CPU.Period = &period
		}, "cpu.max", 10 * time.Second},
		{"canceled", sleeper, func(*initConfig) {}, "receive init result", 500 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := ubuntuInitConfig(t)
			config.CgroupParent = parent
			config.Spec.Linux.CgroupsPath = test.name
			config.Spec.Linux.Resources = testCgroupResources()
			test.change(config)
			ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
			defer cancel()
			err := startInit(ctx, test.executable, config)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if test.name == "canceled" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cancellation error = %v, want DeadlineExceeded", err)
			}
			if _, err := os.Stat(filepath.Join(parent, test.name)); !os.IsNotExist(err) {
				t.Fatalf("cgroup remains after init: %v", err)
			}
		})
	}
	// 已有 cgroup 必须保持完整，创建失败不能把它当作本次的资源删除。
	group, err := cgroup.Create(parent, "existing", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	defer func() {
		if err := group.Remove(); err != nil {
			t.Errorf("remove existing test cgroup: %v", err)
		}
	}()
	config := ubuntuInitConfig(t)
	config.CgroupParent = parent
	config.Spec.Linux.CgroupsPath = "existing"
	if err := startInit(context.Background(), executable, config); err == nil {
		t.Fatal("reused existing cgroup")
	}
	if _, err := os.Stat(filepath.Join(parent, "existing")); err != nil {
		t.Fatalf("existing cgroup was removed: %v", err)
	}
}

func TestStartInitRejectsUnmanagedResources(t *testing.T) {
	for _, configure := range []func(*initConfig){
		func(c *initConfig) { c.Spec.Linux.CgroupsPath = "container" },
		func(c *initConfig) { c.Spec.Linux.Resources = testCgroupResources() },
	} {
		config := testInitConfig(t)
		configure(config)
		if err := startInit(context.Background(), "/minirunc-no-such-executable", config); err == nil || !strings.Contains(err.Error(), "cgroup parent is required") {
			t.Fatalf("unmanaged resources: %v", err)
		}
	}
}
