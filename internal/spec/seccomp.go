package spec

import (
	"fmt"
	"runtime"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// ValidateSeccomp 检查首版支持范围，不查询宿主的系统调用表，也不安装过滤器。
// 仅支持本机 amd64/arm64 ABI，其他 ABI 会由过滤器的 bad-architecture 动作拒绝。
func ValidateSeccomp(config *specs.LinuxSeccomp) error {
	if config == nil {
		return nil
	}
	native := map[string]specs.Arch{"amd64": specs.ArchX86_64, "arm64": specs.ArchAARCH64}[runtime.GOARCH]
	if native == "" {
		return fmt.Errorf("linux.seccomp: native architecture %q is not supported", runtime.GOARCH)
	}
	seenArch := make(map[specs.Arch]bool)
	for i, arch := range config.Architectures {
		if arch != native {
			return fmt.Errorf("linux.seccomp.architectures[%d]: only native architecture %q is supported", i, native)
		}
		if seenArch[arch] {
			return fmt.Errorf("linux.seccomp.architectures[%d]: duplicate architecture", i)
		}
		seenArch[arch] = true
	}
	if len(config.Flags) != 0 {
		return fmt.Errorf("linux.seccomp.flags: explicit filter flags are not supported")
	}
	if config.ListenerPath != "" || config.ListenerMetadata != "" {
		return fmt.Errorf("linux.seccomp: notification listeners are not supported")
	}
	if err := validateSeccompAction(config.DefaultAction, config.DefaultErrnoRet); err != nil {
		return fmt.Errorf("linux.seccomp.defaultAction: %w", err)
	}
	for i, rule := range config.Syscalls {
		field := fmt.Sprintf("linux.seccomp.syscalls[%d]", i)
		if len(rule.Names) == 0 {
			return fmt.Errorf("%s.names: at least one syscall is required", field)
		}
		seenNames := make(map[string]bool)
		for j, name := range rule.Names {
			if name == "" || strings.ContainsRune(name, '\x00') {
				return fmt.Errorf("%s.names[%d]: expected a nonempty syscall name without NUL", field, j)
			}
			if seenNames[name] {
				return fmt.Errorf("%s.names[%d]: duplicate syscall %q", field, j, name)
			}
			seenNames[name] = true
		}
		if err := validateSeccompAction(rule.Action, rule.ErrnoRet); err != nil {
			return fmt.Errorf("%s.action: %w", field, err)
		}
		seenArgs := make(map[uint]bool)
		for j, arg := range rule.Args {
			if arg.Index > 5 {
				return fmt.Errorf("%s.args[%d].index: must be between 0 and 5", field, j)
			}
			// libseccomp 的单条规则不允许重复比较同一参数，首版明确拒绝，不擅自改成 OR。
			if seenArgs[arg.Index] {
				return fmt.Errorf("%s.args[%d].index: repeated argument comparisons are not supported", field, j)
			}
			seenArgs[arg.Index] = true
			switch arg.Op {
			case specs.OpNotEqual, specs.OpLessThan, specs.OpLessEqual, specs.OpEqualTo,
				specs.OpGreaterEqual, specs.OpGreaterThan:
				if arg.ValueTwo != 0 {
					return fmt.Errorf("%s.args[%d].valueTwo: only supported with SCMP_CMP_MASKED_EQ", field, j)
				}
			case specs.OpMaskedEqual:
			default:
				return fmt.Errorf("%s.args[%d].op: unsupported comparison %q", field, j, arg.Op)
			}
		}
	}
	return nil
}

func validateSeccompAction(action specs.LinuxSeccompAction, errnoRet *uint) error {
	switch action {
	case specs.ActAllow, specs.ActKillProcess:
		if errnoRet != nil {
			return fmt.Errorf("errnoRet is only supported with SCMP_ACT_ERRNO")
		}
	case specs.ActErrno:
		// Linux 将超过 MAX_ERRNO 的返回值截断到 4095，拒绝这种隐式改写。
		// 0 是合法配置：跳过系统调用但返回成功，而不是放行系统调用。
		if errnoRet != nil && *errnoRet > 4095 {
			return fmt.Errorf("errnoRet must be between 0 and 4095")
		}
	default:
		return fmt.Errorf("unsupported action %q; supported actions are ALLOW, ERRNO and KILL_PROCESS", action)
	}
	return nil
}
