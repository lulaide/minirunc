package linux

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"reflect"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func TestProcessAttributes(t *testing.T) {
	if mode := os.Getenv("MINIRUNC_PROCESS_TEST_CHILD"); mode != "" {
		testProcessAttributesChild(t, mode)
		return
	}
	uid, gid := os.Getuid(), os.Getgid()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var original unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"limits", "directory", "user", "empty groups"} {
		t.Run(mode, func(t *testing.T) {
			if (mode == "user" || mode == "empty groups") && os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
				t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test identity changes")
			}
			// 所有会改变进程属性的操作都在子进程执行，防止影响后续测试。
			command := exec.Command(os.Args[0], "-test.run=^TestProcessAttributes$")
			command.Env = append(os.Environ(), "MINIRUNC_PROCESS_TEST_CHILD="+mode, "MINIRUNC_PROCESS_TEST_DENIED="+t.TempDir())
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("child: %v\n%s", err, output)
			}
		})
	}
	afterGroups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	afterCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var after unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &after); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != uid || os.Getgid() != gid || !reflect.DeepEqual(groups, afterGroups) || afterCwd != cwd || after != original {
		t.Fatal("child changed parent process attributes")
	}
}

func testProcessAttributesChild(t *testing.T, mode string) {
	switch mode {
	case "limits":
		var before unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &before); err != nil {
			t.Fatal(err)
		}
		// 只降低上限，不依赖提高 hard 所需的特权。
		want := unix.Rlimit{Cur: min(before.Cur, 128), Max: min(before.Max, 256)}
		if err := SetupRlimits([]specs.POSIXRlimit{{Type: "RLIMIT_NOFILE", Soft: want.Cur, Hard: want.Max}}); err != nil {
			t.Fatal(err)
		}
		var got unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &got); err != nil || got != want {
			t.Fatalf("limit = %v, error = %v, want %v", got, err, want)
		}
	case "directory":
		dir := t.TempDir()
		if err := SetupWorkingDirectory(dir); err != nil {
			t.Fatal(err)
		}
		if got, err := os.Getwd(); err != nil || got != dir {
			t.Fatalf("cwd = %q, error = %v", got, err)
		}
		if err := SetupWorkingDirectory(dir + "/missing"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing cwd: %v", err)
		}
		file := dir + "/file"
		if err := os.WriteFile(file, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := SetupWorkingDirectory(file); !errors.Is(err, unix.ENOTDIR) {
			t.Fatalf("file cwd: %v", err)
		}
	case "user", "empty groups":
		// 目录由父进程创建和清理，子进程降权后无法进入或删除它。
		denied := os.Getenv("MINIRUNC_PROCESS_TEST_DENIED")
		user := specs.User{UID: 1000, GID: 1000}
		if mode == "user" {
			user.AdditionalGids = []uint32{1001, 1002}
		}
		if err := SetupUser(user); err != nil {
			t.Fatal(err)
		}
		ruid, euid, suid := unix.Getresuid()
		rgid, egid, sgid := unix.Getresgid()
		if ruid != 1000 || euid != 1000 || suid != 1000 || rgid != 1000 || egid != 1000 || sgid != 1000 {
			t.Fatalf("UIDs = %d/%d/%d, GIDs = %d/%d/%d", ruid, euid, suid, rgid, egid, sgid)
		}
		groups, err := os.Getgroups()
		if err != nil {
			t.Fatal(err)
		}
		if mode == "user" && !reflect.DeepEqual(groups, []int{1001, 1002}) || mode == "empty groups" && len(groups) != 0 {
			t.Fatalf("supplementary groups = %v", groups)
		}
		if err := SetupWorkingDirectory(denied); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("inaccessible cwd: %v", err)
		}
		if err := SetupWorkingDirectory("/tmp"); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown child mode %q", mode)
	}
}

func TestProcessAttributesRejectInvalidConfiguration(t *testing.T) {
	for _, limits := range [][]specs.POSIXRlimit{
		{{Type: "RLIMIT_UNKNOWN"}},
		{{Type: "RLIMIT_CORE", Soft: 1, Hard: 0}},
		{{Type: "RLIMIT_CORE"}, {Type: "RLIMIT_CORE"}},
	} {
		if err := SetupRlimits(limits); err == nil {
			t.Fatalf("accepted invalid limits: %v", limits)
		}
	}
	for _, user := range []specs.User{
		{UID: math.MaxUint32}, {GID: math.MaxUint32}, {AdditionalGids: []uint32{math.MaxUint32}},
	} {
		if err := SetupUser(user); err == nil {
			t.Fatalf("accepted invalid user: %v", user)
		}
	}
	for _, cwd := range []string{"", "relative", "/tmp\x00"} {
		if err := SetupWorkingDirectory(cwd); err == nil {
			t.Fatalf("accepted invalid cwd: %q", cwd)
		}
	}
}
