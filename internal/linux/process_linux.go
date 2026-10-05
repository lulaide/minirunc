package linux

import (
	"fmt"
	"math"
	"os"
	"path"
	"strings"
	"syscall"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

var rlimitResources = map[string]int{
	"RLIMIT_AS": unix.RLIMIT_AS, "RLIMIT_CORE": unix.RLIMIT_CORE,
	"RLIMIT_CPU": unix.RLIMIT_CPU, "RLIMIT_DATA": unix.RLIMIT_DATA,
	"RLIMIT_FSIZE": unix.RLIMIT_FSIZE, "RLIMIT_LOCKS": unix.RLIMIT_LOCKS,
	"RLIMIT_MEMLOCK": unix.RLIMIT_MEMLOCK, "RLIMIT_MSGQUEUE": unix.RLIMIT_MSGQUEUE,
	"RLIMIT_NICE": unix.RLIMIT_NICE, "RLIMIT_NOFILE": unix.RLIMIT_NOFILE,
	"RLIMIT_NPROC": unix.RLIMIT_NPROC, "RLIMIT_RSS": unix.RLIMIT_RSS,
	"RLIMIT_RTPRIO": unix.RLIMIT_RTPRIO, "RLIMIT_RTTIME": unix.RLIMIT_RTTIME,
	"RLIMIT_SIGPENDING": unix.RLIMIT_SIGPENDING, "RLIMIT_STACK": unix.RLIMIT_STACK,
}

// SetupRlimits 设置当前进程的资源限制，应在降低用户权限之前调用。
// soft 是实际生效的限制，hard 是进程自行提高 soft 时不能超过的上限。
// 未配置的限制保持继承值；任一步失败后初始化进程必须退出，不继续执行用户程序。
func SetupRlimits(limits []specs.POSIXRlimit) error {
	// 先检查完整列表，避免配置错误导致前面的限制已经被修改。
	seen := make(map[string]bool, len(limits))
	for i, limit := range limits {
		if _, ok := rlimitResources[limit.Type]; !ok {
			return fmt.Errorf("process.rlimits[%d].type: unknown Linux resource limit %q", i, limit.Type)
		}
		if seen[limit.Type] {
			return fmt.Errorf("process.rlimits[%d].type: duplicate type %q", i, limit.Type)
		}
		seen[limit.Type] = true
		if limit.Soft > limit.Hard {
			return fmt.Errorf("process.rlimits[%d].soft: must not exceed hard", i)
		}
	}
	for _, limit := range limits {
		value := unix.Rlimit{Cur: limit.Soft, Max: limit.Hard}
		// pid=0 表示当前进程。使用 Go 封装的 prlimit，同时更新 Go 对 NOFILE 的记录。
		if err := unix.Prlimit(0, rlimitResources[limit.Type], &value, nil); err != nil {
			return fmt.Errorf("set resource limit %s: %w", limit.Type, err)
		}
	}
	return nil
}

// SetupUser 切换当前进程的用户、主组和附加组，调用前必须完成需要特权的挂载等操作。
// 这里只处理数值身份，不查询 /etc/passwd；Umask 不属于此函数的职责。
// 凭据变更没有整体回滚机制，失败后调用者必须终止初始化进程。
func SetupUser(user specs.User) error {
	if user.UID == math.MaxUint32 || user.GID == math.MaxUint32 {
		return fmt.Errorf("process.user: UID and GID must not be 4294967295")
	}
	groups := make([]int, len(user.AdditionalGids))
	for i, gid := range user.AdditionalGids {
		if gid == math.MaxUint32 {
			return fmt.Errorf("process.user.additionalGids[%d]: must not be 4294967295", i)
		}
		groups[i] = int(gid)
	}
	// 即使列表为空也要清空附加组，避免继承宿主机的组权限。
	// Linux 凭据属于线程；syscall 的 Go 封装会同步各线程，不能用一次原始系统调用代替。
	if err := syscall.Setgroups(groups); err != nil {
		return fmt.Errorf("set supplementary groups: %w", err)
	}
	// 先改组，再改用户，否则降权后可能失去修改组的权限。
	// 三个参数分别是实际、有效、保存的身份；一起修改，避免保留可恢复的原身份。
	gid := int(user.GID)
	if err := syscall.Setresgid(gid, gid, gid); err != nil {
		return fmt.Errorf("set group identity: %w", err)
	}
	uid := int(user.UID)
	if err := syscall.Setresuid(uid, uid, uid); err != nil {
		return fmt.Errorf("set user identity: %w", err)
	}
	return nil
}

// SetupWorkingDirectory 在 rootfs 切换且用户身份设置完成后调用。
// 路径此时相对于容器的根目录；由最终用户的权限决定能否进入，不自动创建目录。
func SetupWorkingDirectory(cwd string) error {
	if !path.IsAbs(cwd) || strings.ContainsRune(cwd, '\x00') {
		return fmt.Errorf("process.cwd: must be an absolute path without NUL")
	}
	if err := os.Chdir(cwd); err != nil {
		return fmt.Errorf("change working directory: %w", err)
	}
	return nil
}
