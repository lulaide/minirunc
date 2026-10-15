package linux

import (
	"errors"
	"fmt"

	"github.com/lulaide/minirunc/internal/spec"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

// SetupCredentials 在挂载和 rlimits 设置后，结合身份切换设置最终能力。
// 调用方必须 LockOSThread，并在这个线程上执行后续初始化及 exec，不能解锁复用。
// capset、bounding、ambient 都是线程属性；其他 Go 线程不会因此被收敛，
// 但不执行用户代码，后续 exec 会销毁它们。任一步失败都必须退出初始化进程。
func SetupCredentials(user specs.User, capabilities *specs.LinuxCapabilities) error {
	sets, err := spec.ParseCapabilities(capabilities)
	if err != nil {
		return err
	}
	last, bounding, err := kernelCapabilities()
	if err != nil {
		return err
	}
	if sets.Bounding & ^bounding != 0 {
		return fmt.Errorf("requested bounding capabilities are not available in the inherited bounding set")
	}
	valid := ^uint64(0) >> (63 - last)
	if (sets.Permitted|sets.Effective|sets.Inheritable|sets.Ambient)&^valid != 0 {
		return fmt.Errorf("requested capabilities are not supported by this kernel")
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var current [2]unix.CapUserData
	if err := unix.Capget(&header, &current[0]); err != nil {
		return fmt.Errorf("read capabilities: %w", err)
	}
	permitted := uint64(current[0].Permitted) | uint64(current[1].Permitted)<<32
	if sets.Permitted & ^permitted != 0 {
		return fmt.Errorf("requested permitted capabilities are not available to the init thread")
	}
	// 必须清掉继承的 ambient，不能把未配置的能力带给用户程序。
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return fmt.Errorf("clear ambient capabilities: %w", err)
	}
	// 先准备 inheritable，再缩小 bounding：内核会限制新增 inheritable 的来源，
	// 过早丢弃 bounding 可能让本来合法的 inheritable 配置无法设置。
	current[0].Inheritable = uint32(sets.Inheritable)
	current[1].Inheritable = uint32(sets.Inheritable >> 32)
	if err := unix.Capset(&header, &current[0]); err != nil {
		return fmt.Errorf("prepare inheritable capabilities: %w", err)
	}
	for number := 0; number <= last; number++ {
		bit := uint64(1) << number
		if bounding&bit != 0 && sets.Bounding&bit == 0 {
			// 丢弃 bounding 不会立刻撤销 effective，因此仍能完成后续身份切换。
			if err := unix.Prctl(unix.PR_CAPBSET_DROP, uintptr(number), 0, 0, 0); err != nil {
				return fmt.Errorf("drop bounding capability %d: %w", number, err)
			}
		}
	}
	keep := 0
	if user.UID != 0 && sets.Permitted != 0 {
		keep = 1
	}
	if err := setKeepCapabilities(keep); err != nil {
		return err
	}
	if err := SetupUser(user); err != nil {
		return err
	}
	// KEEP_CAPS 仅让 permitted 跨越 root -> 非 root 切换，不保留 effective。
	// 设置最终集合前关闭这个临时标志，不给后续程序留下额外身份切换语义。
	if err := setKeepCapabilities(0); err != nil {
		return err
	}
	var final [2]unix.CapUserData
	for i := range final {
		shift := uint(i * 32)
		final[i] = unix.CapUserData{
			Permitted: uint32(sets.Permitted >> shift), Effective: uint32(sets.Effective >> shift),
			Inheritable: uint32(sets.Inheritable >> shift),
		}
	}
	if err := unix.Capset(&header, &final[0]); err != nil {
		return fmt.Errorf("set final capabilities: %w", err)
	}
	for number := 0; number <= last; number++ {
		if sets.Ambient&(uint64(1)<<number) != 0 {
			if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_RAISE, uintptr(number), 0, 0); err != nil {
				return fmt.Errorf("raise ambient capability %d: %w", number, err)
			}
		}
	}
	return nil
}

// SetNoNewPrivileges 阻止后续 exec 通过 setuid 位或文件 capabilities 获得额外权限。
// 它不撤销已有权限，启用后不可关闭，fork 和 exec 都会继承。false 保留继承状态。
// 这是线程属性，同样要求调用方保持初始化线程锁定直到 exec 或退出。
func SetNoNewPrivileges(enabled bool) error {
	if !enabled {
		return nil
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	return nil
}

func setKeepCapabilities(value int) error {
	current, err := unix.PrctlRetInt(unix.PR_GET_KEEPCAPS, 0, 0, 0, 0)
	if err != nil {
		return fmt.Errorf("read keep capabilities: %w", err)
	}
	if current != value {
		if err := unix.Prctl(unix.PR_SET_KEEPCAPS, uintptr(value), 0, 0, 0); err != nil {
			return fmt.Errorf("set keep capabilities: %w", err)
		}
	}
	return nil
}

func kernelCapabilities() (last int, bounding uint64, err error) {
	// 逐项查询而不是依赖容器的 /proc 挂载。遇到 EINVAL 才表示内核编号结束，
	// 其他错误必须上报；运行在更新内核上时也要清除未在配置中命名的新能力。
	for number := 0; ; number++ {
		present, readErr := unix.PrctlRetInt(unix.PR_CAPBSET_READ, uintptr(number), 0, 0, 0)
		if errors.Is(readErr, unix.EINVAL) && number > 0 {
			return number - 1, bounding, nil
		}
		if readErr != nil {
			return 0, 0, fmt.Errorf("read bounding capability %d: %w", number, readErr)
		}
		if number == 64 {
			return 0, 0, fmt.Errorf("kernel capabilities exceed the supported 64-bit capability ABI")
		}
		if present != 0 {
			bounding |= uint64(1) << number
		}
	}
}
