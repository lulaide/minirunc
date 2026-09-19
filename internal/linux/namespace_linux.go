package linux

import (
	"fmt"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

// NamespaceCloneFlags 将 minirunc 能够创建的 namespace 转换为 clone(2) flags。
// 已有 namespace 的路径无法用 clone flags 表示，因此在这里拒绝。
func NamespaceCloneFlags(namespaces []specs.LinuxNamespace) (uintptr, error) {
	var flags uintptr
	seen := make(map[specs.LinuxNamespaceType]bool, len(namespaces))
	for i, namespace := range namespaces {
		if seen[namespace.Type] {
			return 0, fmt.Errorf("linux.namespaces[%d].type: duplicate type %q", i, namespace.Type)
		}
		seen[namespace.Type] = true

		if namespace.Path != "" {
			return 0, fmt.Errorf("linux.namespaces[%d].path: joining an existing %q namespace is not supported", i, namespace.Type)
		}

		var flag int
		switch namespace.Type {
		case specs.PIDNamespace:
			flag = unix.CLONE_NEWPID
		case specs.NetworkNamespace:
			flag = unix.CLONE_NEWNET
		case specs.MountNamespace:
			flag = unix.CLONE_NEWNS
		case specs.IPCNamespace:
			flag = unix.CLONE_NEWIPC
		case specs.UTSNamespace:
			flag = unix.CLONE_NEWUTS
		case specs.CgroupNamespace:
			flag = unix.CLONE_NEWCGROUP
		case specs.UserNamespace:
			return 0, fmt.Errorf("linux.namespaces[%d].type: user namespace creation is not supported", i)
		case specs.TimeNamespace:
			return 0, fmt.Errorf("linux.namespaces[%d].type: time namespace creation is not supported", i)
		default:
			return 0, fmt.Errorf("linux.namespaces[%d].type: unknown type %q", i, namespace.Type)
		}
		flags |= uintptr(flag)
	}
	return flags, nil
}
