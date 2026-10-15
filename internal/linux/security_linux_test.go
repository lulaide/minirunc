package linux

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func TestSecurity(t *testing.T) {
	if mode := os.Getenv("MINIRUNC_SECURITY_TEST_CHILD"); mode != "" {
		runtime.LockOSThread()
		testSecurityChild(t, mode)
		return
	}
	// 所有能力和 prctl 修改都留在子进程，特别是 bounding 和 no_new_privs 无法恢复。
	for _, mode := range []string{"no new privileges", "root empty", "nil config", "nonroot empty", "nonroot retained", "permitted only", "outside bounding", "unavailable bounding", "unavailable permitted", "drop denied"} {
		t.Run(mode, func(t *testing.T) {
			if mode != "no new privileges" && os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
				t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test capability and identity changes")
			}
			command := exec.Command(os.Args[0], "-test.run=^TestSecurity$")
			command.Env = append(os.Environ(), "MINIRUNC_SECURITY_TEST_CHILD="+mode)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("security child: %v\n%s", err, output)
			}
		})
	}
}

func testSecurityChild(t *testing.T, mode string) {
	if mode == "no new privileges" {
		before, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := SetNoNewPrivileges(false); err != nil {
			t.Fatal(err)
		}
		if got, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0); err != nil || got != before {
			t.Fatalf("false changed inherited no_new_privs: %d, error %v", got, err)
		}
		if err := SetNoNewPrivileges(true); err != nil {
			t.Fatal(err)
		}
		if err := SetNoNewPrivileges(false); err != nil {
			t.Fatal(err)
		}
		if got, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0); err != nil || got != 1 {
			t.Fatalf("no_new_privs = %d, error %v", got, err)
		}
		// exec 替换当前测试进程；探针确认标志跨 exec 仍然存在。
		if err := unix.Exec("/proc/self/exe", []string{"security-probe", "-test.run=^TestSecurityExecProbe$"}, []string{"MINIRUNC_SECURITY_EXEC_PROBE=1"}); err != nil {
			t.Fatal(err)
		}
		return
	}
	user := specs.User{}
	caps := &specs.LinuxCapabilities{}
	var want uint32
	if mode == "root empty" {
		// 主动构造继承状态，验证空配置确实清理 ambient 和 KEEP_CAPS，而非碰巧原本为空。
		header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
		var data [2]unix.CapUserData
		if err := unix.Capget(&header, &data[0]); err != nil {
			t.Fatal(err)
		}
		data[0].Inheritable |= 1 << unix.CAP_CHOWN
		if err := unix.Capset(&header, &data[0]); err != nil {
			t.Fatal(err)
		}
		if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_RAISE, unix.CAP_CHOWN, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := unix.Prctl(unix.PR_SET_KEEPCAPS, 1, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "nil config" {
		caps = nil
	}
	if mode == "nonroot empty" || mode == "nonroot retained" {
		user.UID, user.GID = 1000, 1000
	}
	if mode == "nonroot retained" || mode == "outside bounding" {
		names := []string{"CAP_CHOWN"}
		caps = &specs.LinuxCapabilities{Bounding: names, Permitted: names, Effective: names, Inheritable: names, Ambient: names}
		want = 1 << unix.CAP_CHOWN
		if mode == "outside bounding" {
			caps.Bounding = nil
		}
	}
	if mode == "permitted only" {
		caps = &specs.LinuxCapabilities{Bounding: []string{"CAP_CHOWN"}, Permitted: []string{"CAP_CHOWN"}}
		want = 1 << unix.CAP_CHOWN
	}
	if mode == "unavailable bounding" {
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, unix.CAP_CHOWN, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		caps.Bounding = []string{"CAP_CHOWN"}
		if err := SetupCredentials(user, caps); err == nil || !strings.Contains(err.Error(), "inherited bounding") {
			t.Fatalf("unavailable bounding: %v", err)
		}
		return
	}
	if mode == "unavailable permitted" || mode == "drop denied" {
		header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
		var data [2]unix.CapUserData
		if err := unix.Capget(&header, &data[0]); err != nil {
			t.Fatal(err)
		}
		bit := uint32(1 << unix.CAP_CHOWN)
		if mode == "drop denied" {
			bit = 1 << unix.CAP_SETPCAP
		}
		data[0].Permitted &^= bit
		data[0].Effective &^= bit
		if err := unix.Capset(&header, &data[0]); err != nil {
			t.Fatal(err)
		}
		if mode == "unavailable permitted" {
			caps.Permitted = []string{"CAP_CHOWN"}
			if err := SetupCredentials(user, caps); err == nil || !strings.Contains(err.Error(), "permitted capabilities are not available") {
				t.Fatalf("unavailable permitted: %v", err)
			}
		} else if err := SetupCredentials(user, caps); !errors.Is(err, unix.EPERM) {
			t.Fatalf("drop bounding without CAP_SETPCAP: %v", err)
		}
		return
	}
	if err := SetupCredentials(user, caps); err != nil {
		t.Fatal(err)
	}
	if err := SetNoNewPrivileges(true); err != nil {
		t.Fatal(err)
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&header, &data[0]); err != nil {
		t.Fatal(err)
	}
	wantData := unix.CapUserData{Permitted: want, Effective: want, Inheritable: want}
	wantAmbient := want
	if mode == "permitted only" {
		wantData.Effective, wantData.Inheritable, wantAmbient = 0, 0, 0
	}
	if data[0] != wantData || data[1] != (unix.CapUserData{}) {
		t.Fatalf("final capabilities = %+v, want low mask %#x", data, want)
	}
	last, bounding, err := kernelCapabilities()
	wantBounding := uint64(want)
	if mode == "outside bounding" {
		wantBounding = 0
	}
	if err != nil || bounding != wantBounding {
		t.Fatalf("bounding = %#x, error %v, want %#x", bounding, err, wantBounding)
	}
	for number := 0; number <= last; number++ {
		got, err := unix.PrctlRetInt(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_IS_SET, uintptr(number), 0, 0)
		if err != nil || (got == 1) != (uint64(wantAmbient)&(uint64(1)<<number) != 0) {
			t.Fatalf("ambient capability %d = %d, error %v", number, got, err)
		}
	}
	if got, err := unix.PrctlRetInt(unix.PR_GET_KEEPCAPS, 0, 0, 0, 0); err != nil || got != 0 {
		t.Fatalf("keep capabilities = %d, error %v", got, err)
	}
	ruid, euid, suid := unix.Getresuid()
	if ruid != int(user.UID) || euid != int(user.UID) || suid != int(user.UID) {
		t.Fatalf("UIDs = %d/%d/%d", ruid, euid, suid)
	}
	if user.UID != 0 {
		if err := unix.Setresuid(0, 0, 0); !errors.Is(err, unix.EPERM) {
			t.Fatalf("restored root identity: %v", err)
		}
	}
	if mode == "nonroot retained" {
		if err := unix.Exec("/proc/self/exe", []string{"security-probe", "-test.run=^TestSecurityExecProbe$"}, []string{
			"MINIRUNC_SECURITY_EXEC_PROBE=1", "MINIRUNC_SECURITY_EXEC_CAP=1",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSecurityExecProbe(t *testing.T) {
	if os.Getenv("MINIRUNC_SECURITY_EXEC_PROBE") != "1" {
		t.Skip("only used by the security exec test")
	}
	if got, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0); err != nil || got != 1 {
		t.Fatalf("no_new_privs after exec = %d, error %v", got, err)
	}
	if os.Getenv("MINIRUNC_SECURITY_EXEC_CAP") == "1" {
		header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
		var data [2]unix.CapUserData
		if err := unix.Capget(&header, &data[0]); err != nil || data[0] != (unix.CapUserData{Permitted: 1, Effective: 1, Inheritable: 1}) || data[1] != (unix.CapUserData{}) {
			t.Fatalf("capabilities after exec = %+v, error %v", data, err)
		}
		if os.Getuid() != 1000 || os.Geteuid() != 1000 {
			t.Fatalf("unexpected identity after exec: %d/%d", os.Getuid(), os.Geteuid())
		}
	}
}
