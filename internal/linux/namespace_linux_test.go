package linux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func TestNamespaceCloneFlags(t *testing.T) {
	namespaces := []specs.LinuxNamespace{
		{Type: specs.PIDNamespace},
		{Type: specs.NetworkNamespace},
		{Type: specs.MountNamespace},
		{Type: specs.IPCNamespace},
		{Type: specs.UTSNamespace},
		{Type: specs.CgroupNamespace},
	}
	flags, err := NamespaceCloneFlags(namespaces)
	if err != nil {
		t.Fatal(err)
	}
	want := uintptr(unix.CLONE_NEWPID | unix.CLONE_NEWNET | unix.CLONE_NEWNS |
		unix.CLONE_NEWIPC | unix.CLONE_NEWUTS | unix.CLONE_NEWCGROUP)
	if flags != want {
		t.Fatalf("clone flags = %#x, want %#x", flags, want)
	}
	if flags, err := NamespaceCloneFlags(nil); err != nil || flags != 0 {
		t.Fatalf("empty clone flags = %#x, error = %v", flags, err)
	}
}

func TestNamespaceCloneFlagsRejectsUnsupportedConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		namespaces []specs.LinuxNamespace
		message    string
	}{
		{"existing namespace", []specs.LinuxNamespace{{Type: specs.PIDNamespace, Path: "/proc/1/ns/pid"}}, "joining an existing"},
		{"duplicate namespace", []specs.LinuxNamespace{{Type: specs.UTSNamespace}, {Type: specs.UTSNamespace}}, "duplicate"},
		{"user namespace", []specs.LinuxNamespace{{Type: specs.UserNamespace}}, "user namespace creation"},
		{"time namespace", []specs.LinuxNamespace{{Type: specs.TimeNamespace}}, "time namespace creation"},
		{"unknown namespace", []specs.LinuxNamespace{{Type: "unknown"}}, "unknown type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, err := NamespaceCloneFlags(tt.namespaces)
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("clone flags = %#x, error = %v, want %q", flags, err, tt.message)
			}
			if flags != 0 {
				t.Fatalf("clone flags = %#x on error, want 0", flags)
			}
		})
	}
}

func TestNamespaceCreation(t *testing.T) {
	procNames := []string{"mnt", "pid", "uts", "ipc", "net", "cgroup"}
	if os.Getenv("MINIRUNC_NAMESPACE_TEST_CHILD") == "1" {
		fmt.Printf("process_pid=%d\n", os.Getpid())
		for _, name := range procNames {
			value, err := os.Readlink("/proc/self/ns/" + name)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Printf("%s=%s\n", name, value)
		}
		os.Exit(0)
	}
	if os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
		t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test namespace creation")
	}

	namespaces := make([]specs.LinuxNamespace, 0, len(procNames))
	for _, namespaceType := range []specs.LinuxNamespaceType{
		specs.MountNamespace, specs.PIDNamespace, specs.UTSNamespace,
		specs.IPCNamespace, specs.NetworkNamespace, specs.CgroupNamespace,
	} {
		namespaces = append(namespaces, specs.LinuxNamespace{Type: namespaceType})
	}
	flags, err := NamespaceCloneFlags(namespaces)
	if err != nil {
		t.Fatal(err)
	}

	hostNamespaces := make(map[string]string, len(procNames))
	for _, name := range procNames {
		hostNamespaces[name], err = os.Readlink("/proc/self/ns/" + name)
		if err != nil {
			t.Fatal(err)
		}
	}

	command := exec.Command(os.Args[0], "-test.run=^TestNamespaceCreation$")
	command.Env = append(os.Environ(), "MINIRUNC_NAMESPACE_TEST_CHILD=1")
	command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: flags}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("start namespace child: %v\n%s", err, output)
	}
	values := parseNamespaceOutput(t, string(output))
	if values["process_pid"] != "1" {
		t.Fatalf("container PID = %q, want 1; output:\n%s", values["process_pid"], output)
	}
	for _, name := range procNames {
		if values[name] == "" || values[name] == hostNamespaces[name] {
			t.Errorf("%s namespace = %q, host = %q", name, values[name], hostNamespaces[name])
		}
	}
}

func parseNamespaceOutput(t *testing.T, output string) map[string]string {
	t.Helper()
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("invalid namespace child output %q", line)
		}
		values[key] = value
	}
	return values
}
