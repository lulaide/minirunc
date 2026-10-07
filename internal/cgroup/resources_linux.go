package cgroup

import (
	"fmt"
	"os"
	"strconv"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

type limitSetting struct {
	file  string
	value string
}

// ApplyLimits 为已经创建、尚未加入进程的 cgroup v2 设置资源限制。
// 调用者负责选择有权限管理的目录、启用控制器及失败后的清理。
// 这里不实现运行中的资源更新：多次写入不是事务，失败时已有设置不会自动回滚。
func ApplyLimits(directory string, resources *specs.LinuxResources) error {
	settings, err := resourceSettings(resources)
	if err != nil {
		return err
	}
	dir, err := openDirectory(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return applySettings(dir, settings)
}

func openDirectory(directory string) (*os.File, error) {
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open cgroup directory: %w", err)
	}
	dir := os.NewFile(uintptr(fd), directory)
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("inspect cgroup filesystem: %w", err)
	}
	if fs.Type != unix.CGROUP2_SUPER_MAGIC {
		_ = dir.Close()
		return nil, fmt.Errorf("cgroup directory must be on a cgroup v2 filesystem")
	}
	return dir, nil
}

func applySettings(directory *os.File, settings []limitSetting) error {
	for _, setting := range settings {
		if err := writeControl(directory, setting.file, setting.value); err != nil {
			return err
		}
	}
	return nil
}

func writeControl(directory *os.File, name, value string) error {
	// 控制文件由内核提供，不使用 O_CREATE；缺少文件意味着控制器或权限不满足要求。
	// 使用目录 fd 和内部固定文件名，避免写入时重新解析整个目录路径。
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open cgroup control %s: %w", name, err)
	}
	file := os.NewFile(uintptr(fd), name)
	_, writeErr := file.WriteString(value)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write cgroup control %s: %w", name, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close cgroup control %s: %w", name, closeErr)
	}
	return nil
}

func resourceSettings(resources *specs.LinuxResources) ([]limitSetting, error) {
	if resources == nil {
		return nil, nil
	}
	// 明确当前支持范围，不能静默忽略配置中的限制。
	if len(resources.Devices) != 0 || resources.BlockIO != nil || len(resources.HugepageLimits) != 0 ||
		resources.Network != nil || len(resources.Rdma) != 0 || len(resources.Unified) != 0 {
		return nil, fmt.Errorf("linux.resources: only memory.limit, cpu.quota/period and pids.limit are supported")
	}
	var settings []limitSetting
	if memory := resources.Memory; memory != nil {
		if memory.Reservation != nil || memory.Swap != nil || memory.Kernel != nil || memory.KernelTCP != nil ||
			memory.Swappiness != nil || memory.DisableOOMKiller != nil || memory.UseHierarchy != nil || memory.CheckBeforeUpdate != nil {
			return nil, fmt.Errorf("linux.resources.memory: only limit is supported")
		}
		if memory.Limit != nil {
			value, err := maxValue(*memory.Limit)
			if err != nil {
				return nil, fmt.Errorf("linux.resources.memory.limit: %w", err)
			}
			settings = append(settings, limitSetting{"memory.max", value})
		}
	}
	if cpu := resources.CPU; cpu != nil {
		if cpu.Shares != nil || cpu.Burst != nil || cpu.RealtimeRuntime != nil || cpu.RealtimePeriod != nil ||
			cpu.Cpus != "" || cpu.Mems != "" || cpu.Idle != nil {
			return nil, fmt.Errorf("linux.resources.cpu: only quota and period are supported")
		}
		if cpu.Quota != nil || cpu.Period != nil {
			// 新建 cgroup 的 cpu.max 默认是 "max 100000"。
			// quota/period 单位都是微秒；50000/100000 相当于半个 CPU 的时间预算。
			quota, period := "max", uint64(100000)
			if cpu.Quota != nil {
				if *cpu.Quota == 0 {
					return nil, fmt.Errorf("linux.resources.cpu.quota: must be positive or -1")
				}
				var err error
				quota, err = maxValue(*cpu.Quota)
				if err != nil {
					return nil, fmt.Errorf("linux.resources.cpu.quota: %w", err)
				}
			}
			if cpu.Period != nil {
				if *cpu.Period == 0 {
					return nil, fmt.Errorf("linux.resources.cpu.period: must be positive")
				}
				period = *cpu.Period
			}
			settings = append(settings, limitSetting{"cpu.max", quota + " " + strconv.FormatUint(period, 10)})
		}
	}
	if pids := resources.Pids; pids != nil && pids.Limit != nil {
		value, err := maxValue(*pids.Limit)
		if err != nil {
			return nil, fmt.Errorf("linux.resources.pids.limit: %w", err)
		}
		settings = append(settings, limitSetting{"pids.max", value})
	}
	return settings, nil
}

func maxValue(value int64) (string, error) {
	if value == -1 {
		return "max", nil
	}
	if value < 0 {
		return "", fmt.Errorf("must be non-negative or -1")
	}
	return strconv.FormatInt(value, 10), nil
}
