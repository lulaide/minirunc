package linux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/lulaide/minirunc/internal/spec"
	"golang.org/x/sys/unix"
)

func TestParseMountOptions(t *testing.T) {
	flags, propagation, data, err := parseMountOptions([]string{
		"nosuid", "noexec", "nodev", "strictatime", "mode=755", "size=65536k", "rprivate",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantFlags := uintptr(unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV | unix.MS_STRICTATIME)
	if flags != wantFlags {
		t.Errorf("flags = %#x, want %#x", flags, wantFlags)
	}
	if propagation != uintptr(unix.MS_PRIVATE|unix.MS_REC) {
		t.Errorf("propagation = %#x, want private recursive", propagation)
	}
	if data != "mode=755,size=65536k" {
		t.Errorf("filesystem data = %q", data)
	}
}

func TestParseMountOptionsLastToggleWins(t *testing.T) {
	flags, _, _, err := parseMountOptions([]string{"ro", "rw", "nosuid", "suid", "relatime", "strictatime"})
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.MS_RDONLY != 0 || flags&unix.MS_NOSUID != 0 {
		t.Errorf("cleared flags remain set: %#x", flags)
	}
	if flags&unix.MS_STRICTATIME == 0 || flags&unix.MS_RELATIME != 0 {
		t.Errorf("atime flags = %#x, want strictatime only", flags)
	}
}

func TestParseMountOptionsRejectsUnsupportedOptions(t *testing.T) {
	for _, options := range [][]string{{"bind"}, {"private", "shared"}, {"value=bad\x00option"}} {
		if _, _, _, err := parseMountOptions(options); err == nil {
			t.Fatalf("options %q: expected error", options)
		}
	}
}

func TestSetupRootfsWithUbuntuBundle(t *testing.T) {
	if os.Getenv("MINIRUNC_ROOTFS_TEST_CHILD") == "1" {
		runRootfsTestChild()
		os.Exit(0)
	}
	if os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
		t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test rootfs setup")
	}
	bundleDir := os.Getenv("MINIRUNC_TEST_BUNDLE")
	if bundleDir == "" {
		_, filename, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("resolve test source path")
		}
		bundleDir = filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "bundles", "ubuntu-24.04")
	}
	bundle, err := spec.LoadBundle(bundleDir)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := NamespaceCloneFlags(bundle.Spec.Linux.Namespaces)
	if err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestSetupRootfsWithUbuntuBundle$")
	command.Env = append(os.Environ(),
		"MINIRUNC_ROOTFS_TEST_CHILD=1",
		"MINIRUNC_TEST_BUNDLE="+bundleDir,
	)
	command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: flags}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("set up Ubuntu rootfs: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "rootfs checks passed") {
		t.Fatalf("unexpected child output: %s", output)
	}
	if _, err := os.Stat(filepath.Join(bundle.RootfsPath, oldRootDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old root directory remains after child exit: %v", err)
	}
}

func runRootfsTestChild() {
	bundle, err := spec.LoadBundle(os.Getenv("MINIRUNC_TEST_BUNDLE"))
	if err != nil {
		rootfsTestFatal(err)
	}
	linuxConfig := bundle.Spec.Linux
	if linuxConfig == nil {
		rootfsTestFatal(errors.New("linux config is required"))
	}
	err = SetupRootfs(RootfsConfig{
		Path:          bundle.RootfsPath,
		Readonly:      true,
		Mounts:        bundle.Spec.Mounts,
		MaskedPaths:   linuxConfig.MaskedPaths,
		ReadonlyPaths: linuxConfig.ReadonlyPaths,
	})
	if err != nil {
		rootfsTestFatal(err)
	}
	data, err := os.ReadFile("/etc/os-release")
	if err != nil || !strings.Contains(string(data), "Ubuntu 24.04") {
		rootfsTestFatal(fmt.Errorf("unexpected os-release: %w", err))
	}
	var procStat unix.Statfs_t
	if err := unix.Statfs("/proc", &procStat); err != nil || procStat.Type != unix.PROC_SUPER_MAGIC {
		rootfsTestFatal(fmt.Errorf("/proc is not procfs: type=%#x error=%w", procStat.Type, err))
	}
	var nullStat unix.Stat_t
	if err := unix.Stat("/dev/null", &nullStat); err != nil || nullStat.Mode&unix.S_IFMT != unix.S_IFCHR {
		rootfsTestFatal(fmt.Errorf("/dev/null is not a character device: error=%w", err))
	}
	var maskedStat unix.Stat_t
	if err := unix.Stat("/proc/kcore", &maskedStat); err != nil ||
		maskedStat.Mode&unix.S_IFMT != unix.S_IFCHR || unix.Major(uint64(maskedStat.Rdev)) != 1 || unix.Minor(uint64(maskedStat.Rdev)) != 3 {
		rootfsTestFatal(fmt.Errorf("/proc/kcore is not masked by /dev/null: error=%w", err))
	}
	if !mountpointIsReadonly("/proc/sys") {
		rootfsTestFatal(errors.New("/proc/sys is not readonly"))
	}
	if !mountpointIsReadonly("/") {
		rootfsTestFatal(errors.New("rootfs is not readonly"))
	}
	if _, err := os.Stat("/" + oldRootDir); !errors.Is(err, os.ErrNotExist) {
		rootfsTestFatal(errors.New("old root is still reachable"))
	}
	fmt.Println("rootfs checks passed")
}

func mountpointIsReadonly(path string) bool {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 5 && fields[4] == path {
			for _, option := range strings.Split(fields[5], ",") {
				if option == "ro" {
					return true
				}
			}
		}
	}
	return false
}

func rootfsTestFatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
