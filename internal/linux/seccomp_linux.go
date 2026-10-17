package linux

import (
	"fmt"

	"github.com/lulaide/minirunc/internal/spec"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	seccomp "github.com/seccomp/libseccomp-golang"
	"golang.org/x/sys/unix"
)

// SetupSeccomp 编译 OCI 规则并安装过滤器。调用方必须锁定初始化线程，
// 在身份、capabilities、no_new_privs 和 cwd 设置完成后，紧邻最终执行阶段调用。
// 不额外放行任何启动所需系统调用；规则阻止通信或退出时，父进程必须按失败处理。
func SetupSeccomp(config *specs.LinuxSeccomp) error {
	if config == nil {
		return nil
	}
	if err := spec.ValidateSeccomp(config); err != nil {
		return err
	}
	// Go 绑定创建过滤器时默认请求 TSYNC（API 2）；KILL_PROCESS 需要 API 3。
	// 要求 API >= 3，避免旧环境静默退化为单线程或不支持整个进程终止。
	api, err := seccomp.GetAPI()
	if err != nil {
		return fmt.Errorf("query seccomp API: %w", err)
	}
	if api < 3 {
		return fmt.Errorf("seccomp requires API level 3 or later for thread synchronization and KILL_PROCESS")
	}
	defaultAction := seccompAction(config.DefaultAction, config.DefaultErrnoRet)
	filter, err := seccomp.NewFilter(defaultAction)
	if err != nil {
		return fmt.Errorf("create seccomp filter: %w", err)
	}
	defer filter.Release()
	// libseccomp 默认会自行开启 NNP。关闭这个行为，保留 OCI 的明确选择：
	// NNP 未启用且当前线程无 CAP_SYS_ADMIN 时，安装必须报错，不能偷偷改变安全配置。
	if err := filter.SetNoNewPrivsBit(false); err != nil {
		return fmt.Errorf("configure seccomp no_new_privs handling: %w", err)
	}
	if err := filter.SetBadArchAction(seccomp.ActKillProcess); err != nil {
		return fmt.Errorf("configure seccomp bad-architecture action: %w", err)
	}
	for i, rule := range config.Syscalls {
		action := seccompAction(rule.Action, rule.ErrnoRet)
		conditions := make([]seccomp.ScmpCondition, 0, len(rule.Args))
		for j, arg := range rule.Args {
			values := []uint64{arg.Value}
			if arg.Op == specs.OpMaskedEqual {
				// MASKED_EQ: (参数 & value) == valueTwo；value 是掩码，不是比较值。
				values = append(values, arg.ValueTwo)
			}
			condition, err := seccomp.MakeCondition(arg.Index, seccompOperators[arg.Op], values...)
			if err != nil {
				return fmt.Errorf("compile seccomp syscalls[%d].args[%d]: %w", i, j, err)
			}
			conditions = append(conditions, condition)
		}
		for _, name := range rule.Names {
			call, err := seccomp.GetSyscallFromName(name)
			if err != nil {
				return fmt.Errorf("resolve seccomp syscalls[%d] %q: %w", i, name, err)
			}
			// 和默认动作完全相同的规则不会改变结果，但名称仍须解析，不能忽略拼写错误。
			if action == defaultAction {
				continue
			}
			// 同一规则的不同参数是 AND；多条规则由 libseccomp 合并。
			if err := filter.AddRuleConditionalExact(call, action, conditions); err != nil {
				return fmt.Errorf("compile seccomp syscalls[%d] %q: %w", i, name, err)
			}
		}
	}
	if err := filter.Load(); err != nil {
		return fmt.Errorf("load seccomp filter: %w", err)
	}
	return nil
}

var seccompOperators = map[specs.LinuxSeccompOperator]seccomp.ScmpCompareOp{
	specs.OpNotEqual: seccomp.CompareNotEqual, specs.OpLessThan: seccomp.CompareLess,
	specs.OpLessEqual: seccomp.CompareLessOrEqual, specs.OpEqualTo: seccomp.CompareEqual,
	specs.OpGreaterEqual: seccomp.CompareGreaterEqual, specs.OpGreaterThan: seccomp.CompareGreater,
	specs.OpMaskedEqual: seccomp.CompareMaskedEqual,
}

func seccompAction(action specs.LinuxSeccompAction, errnoRet *uint) seccomp.ScmpAction {
	switch action {
	case specs.ActAllow:
		return seccomp.ActAllow
	case specs.ActKillProcess:
		return seccomp.ActKillProcess
	default: // ValidateSeccomp 已保证这里只能是 ERRNO。
		code := int16(unix.EPERM)
		if errnoRet != nil {
			code = int16(*errnoRet)
		}
		return seccomp.ActErrno.SetReturnCode(code)
	}
}
