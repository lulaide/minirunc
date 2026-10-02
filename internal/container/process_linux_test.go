package container

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lulaide/minirunc/internal/spec"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func TestStartInitWithUbuntuBundle(t *testing.T) {
	if os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
		t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test init isolation")
	}
	executable := buildInitExecutable(t)
	config := ubuntuInitConfig(t)
	hostMount := openTestNamespace(t, "mnt")
	hostUTS := openTestNamespace(t, "uts")
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*initConfig)
		want   string
	}{
		{"successful isolation", func(*initConfig) {}, ""},
		{"large configuration", func(c *initConfig) {
			c.Spec.Process.Env = append(c.Spec.Process.Env, "PAYLOAD="+strings.Repeat("x", 256*1024))
		}, ""},
		{"missing rootfs", func(c *initConfig) { c.RootfsPath = filepath.Join(t.TempDir(), "missing") }, "stat rootfs"},
		{"unsupported mount", func(c *initConfig) {
			c.Spec.Mounts = []specs.Mount{{Destination: "/unsupported", Type: "bind"}}
		}, "filesystem type \"bind\" is not supported"},
		{"invalid mount options", func(c *initConfig) {
			c.Spec.Mounts = []specs.Mount{{Destination: "/proc", Type: "proc", Source: "proc", Options: []string{"bind"}}}
		}, "mount option \"bind\" is not supported"},
		{"invalid hostname", func(c *initConfig) { c.Spec.Hostname = strings.Repeat("x", 256) }, "set hostname"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := ubuntuInitConfig(t)
			test.change(config)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := startInit(ctx, executable, config)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	for name, original := range map[string]*os.File{"mnt": hostMount, "uts": hostUTS} {
		originalInfo, err := original.Stat()
		if err != nil {
			t.Fatal(err)
		}
		currentInfo, err := os.Stat("/proc/self/ns/" + name)
		if err != nil || !os.SameFile(originalInfo, currentInfo) {
			t.Fatalf("parent %s namespace changed: %v", name, err)
		}
	}
	if current, err := os.Hostname(); err != nil || current != hostname {
		t.Fatalf("parent hostname changed: %q, error %v", current, err)
	}
	if _, err := os.Stat(filepath.Join(config.RootfsPath, ".minirunc-oldroot")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old root directory remains: %v", err)
	}
}

func TestStartInitRejectsMissingResult(t *testing.T) {
	if os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
		t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test init isolation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// /bin/true 只会成功退出，不返回控制消息。退出码 0 不能代替确认消息。
	err := startInit(ctx, "/bin/true", ubuntuInitConfig(t))
	if err == nil || (!strings.Contains(err.Error(), "receive init result") && !errors.Is(err, unix.EPIPE)) {
		t.Fatalf("error = %v, want missing result after child startup", err)
	}
}

func TestStartInitCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := startInit(ctx, "/proc/self/exe", testInitConfig(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestHandleInitIgnoresNormalCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"validate"}, {"--help"}, {initCommand, "extra"}} {
		if handled, _ := HandleInit(args); handled {
			t.Fatalf("normal invocation was handled as init: %v", args)
		}
	}
}

func TestStartInitRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*initConfig)
		want   string
	}{
		{"missing spec", func(c *initConfig) { c.Spec = nil }, "OCI spec is required"},
		{"relative rootfs", func(c *initConfig) { c.RootfsPath = "relative" }, "rootfsPath"},
		{"invalid spec", func(c *initConfig) { c.Spec.Version = "" }, "ociVersion"},
		{"missing linux", func(c *initConfig) { c.Spec.Linux = nil }, "linux: configuration is required"},
		{"missing mount namespace", func(c *initConfig) {
			c.Spec.Linux.Namespaces = []specs.LinuxNamespace{{Type: specs.UTSNamespace}}
		}, "a new mount namespace is required"},
		{"missing UTS namespace", func(c *initConfig) {
			c.Spec.Linux.Namespaces = []specs.LinuxNamespace{{Type: specs.MountNamespace}}
		}, "a new UTS namespace is required"},
		{"existing namespace", func(c *initConfig) {
			c.Spec.Linux.Namespaces[0].Path = "/proc/self/ns/mnt"
		}, "joining an existing"},
		{"user namespace", func(c *initConfig) {
			c.Spec.Linux.Namespaces = append(c.Spec.Linux.Namespaces, specs.LinuxNamespace{Type: specs.UserNamespace})
		}, "user namespace creation is not supported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := testInitConfig(t)
			test.change(config)
			// 无效的可执行路径确保这些错误在创建子进程之前就被发现。
			err := startInit(context.Background(), "/minirunc-test-no-such-executable", config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	if err := startInit(context.Background(), "/minirunc-test-no-such-executable", nil); err == nil {
		t.Fatal("nil configuration was accepted")
	}
}

func TestStartInitPermissionDenied(t *testing.T) {
	if os.Getenv("MINIRUNC_INIT_NO_CAP_TEST_CHILD") == "1" {
		// 只在独立测试子进程里撤掉 CAP_SYS_ADMIN，宿主测试进程保留原权限。
		// capabilities 按线程生效，锁定线程确保随后 clone 继承这组权限。
		runtime.LockOSThread()
		header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
		var caps [2]unix.CapUserData
		if err := unix.Capget(&header, &caps[0]); err != nil {
			t.Fatal(err)
		}
		bit := uint32(1) << unix.CAP_SYS_ADMIN
		caps[0].Effective &^= bit
		caps[0].Permitted &^= bit
		caps[0].Inheritable &^= bit
		if err := unix.Capset(&header, &caps[0]); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := startInit(ctx, "/proc/self/exe", testInitConfig(t)); !errors.Is(err, unix.EPERM) {
			t.Fatalf("namespace creation without CAP_SYS_ADMIN: %v, want EPERM", err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartInitPermissionDenied$")
	child.Env = append(os.Environ(), "MINIRUNC_INIT_NO_CAP_TEST_CHILD=1")
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("check namespace permission error: %v\n%s", err, output)
	}
}

func testInitConfig(t *testing.T) *initConfig {
	t.Helper()
	return &initConfig{
		RootfsPath: t.TempDir(),
		Spec: &specs.Spec{
			Version:  spec.OCIVersion,
			Root:     &specs.Root{Path: "rootfs"},
			Process:  &specs.Process{Args: []string{"/bin/sh"}, Cwd: "/"},
			Hostname: "minirunc-init-test",
			Linux: &specs.Linux{Namespaces: []specs.LinuxNamespace{
				{Type: specs.MountNamespace}, {Type: specs.UTSNamespace},
			}},
		},
	}
}

func ubuntuInitConfig(t *testing.T) *initConfig {
	t.Helper()
	bundleDir := os.Getenv("MINIRUNC_TEST_BUNDLE")
	if bundleDir == "" {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("resolve test source path")
		}
		bundleDir = filepath.Join(filepath.Dir(source), "..", "..", "testdata", "bundles", "ubuntu-24.04")
	}
	bundle, err := spec.LoadBundle(bundleDir)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Spec.Root.Readonly = true
	return &initConfig{RootfsPath: bundle.RootfsPath, Spec: bundle.Spec}
}

func openTestNamespace(t *testing.T, name string) *os.File {
	t.Helper()
	file, err := os.Open("/proc/self/ns/" + name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func buildInitExecutable(t *testing.T) string {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "minirunc")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", executable, "../..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build minirunc: %v\n%s", err, output)
	}
	return executable
}
