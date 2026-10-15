package spec

import (
	"fmt"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// CapabilitySets 用位图表示五个能力集合，第 n 位对应 Linux 能力编号 n。
// 解析不访问当前内核；能力能否在宿主上获得，由初始化阶段另外检查。
type CapabilitySets struct {
	Bounding, Permitted, Effective, Inheritable, Ambient uint64
}

// 顺序对应 Linux UAPI 的能力编号，不能按名称排序。
var capabilityNames = [...]string{
	"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_DAC_READ_SEARCH", "CAP_FOWNER",
	"CAP_FSETID", "CAP_KILL", "CAP_SETGID", "CAP_SETUID", "CAP_SETPCAP",
	"CAP_LINUX_IMMUTABLE", "CAP_NET_BIND_SERVICE", "CAP_NET_BROADCAST",
	"CAP_NET_ADMIN", "CAP_NET_RAW", "CAP_IPC_LOCK", "CAP_IPC_OWNER",
	"CAP_SYS_MODULE", "CAP_SYS_RAWIO", "CAP_SYS_CHROOT", "CAP_SYS_PTRACE",
	"CAP_SYS_PACCT", "CAP_SYS_ADMIN", "CAP_SYS_BOOT", "CAP_SYS_NICE",
	"CAP_SYS_RESOURCE", "CAP_SYS_TIME", "CAP_SYS_TTY_CONFIG", "CAP_MKNOD",
	"CAP_LEASE", "CAP_AUDIT_WRITE", "CAP_AUDIT_CONTROL", "CAP_SETFCAP",
	"CAP_MAC_OVERRIDE", "CAP_MAC_ADMIN", "CAP_SYSLOG", "CAP_WAKE_ALARM",
	"CAP_BLOCK_SUSPEND", "CAP_AUDIT_READ", "CAP_PERFMON", "CAP_BPF",
	"CAP_CHECKPOINT_RESTORE",
}

// ParseCapabilities 将 OCI 名称转换成位图并检查内核要求的集合关系。
// minirunc 对省略配置采用空集合策略，不沿用宿主启动者的能力。
func ParseCapabilities(config *specs.LinuxCapabilities) (CapabilitySets, error) {
	var result CapabilitySets
	if config == nil {
		return result, nil
	}
	for _, set := range []struct {
		name   string
		values []string
		mask   *uint64
	}{
		{"bounding", config.Bounding, &result.Bounding},
		{"permitted", config.Permitted, &result.Permitted},
		{"effective", config.Effective, &result.Effective},
		{"inheritable", config.Inheritable, &result.Inheritable},
		{"ambient", config.Ambient, &result.Ambient},
	} {
		for i, name := range set.values {
			var bit uint64
			for number, known := range capabilityNames {
				if name == known {
					bit = uint64(1) << number
					break
				}
			}
			if bit == 0 {
				return result, fmt.Errorf("process.capabilities.%s[%d]: unknown capability %q", set.name, i, name)
			}
			if *set.mask&bit != 0 {
				return result, fmt.Errorf("process.capabilities.%s[%d]: duplicate capability %q", set.name, i, name)
			}
			*set.mask |= bit
		}
	}
	if result.Effective & ^result.Permitted != 0 {
		return result, fmt.Errorf("process.capabilities.effective: must be a subset of permitted")
	}
	if result.Ambient & ^(result.Permitted&result.Inheritable) != 0 {
		return result, fmt.Errorf("process.capabilities.ambient: must be a subset of both permitted and inheritable")
	}
	return result, nil
}
