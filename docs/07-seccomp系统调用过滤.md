# seccomp 系统调用过滤

## 过滤什么

seccomp 让内核在执行系统调用前检查过滤器，根据系统调用编号、架构和参数决定放行、返回错误或终止进程。它不代替 namespace、文件权限和 capabilities，也不能根据路径字符串的内容判断能否打开某个文件：过滤器看到的是指针数值，不能读取指针指向的内容。

OCI 使用 `linux.seccomp` 描述策略。`defaultAction` 用于没有命中规则的调用；`syscalls` 按名称配置例外。同一规则中的参数条件是 AND，多条规则由 libseccomp 合并，不按 JSON 中的先后顺序逐条执行。

## 当前支持范围

| 配置 | 支持范围 |
| --- | --- |
| 架构 | 当前机器的 amd64 或 arm64 原生 ABI；省略 architectures 时也是原生 ABI |
| 动作 | `SCMP_ACT_ALLOW`、`SCMP_ACT_ERRNO`、`SCMP_ACT_KILL_PROCESS` |
| 参数 | 索引 0～5，支持 NE、LT、LE、EQ、GE、GT、MASKED_EQ；单条规则不能重复比较同一参数 |
| 其他 | 不支持显式 flags、通知监听器和其他动作；不自动提供默认策略 |

`ERRNO` 会跳过系统调用并返回指定错误码，省略 `errnoRet` 时为 EPERM。允许范围为 0～4095；0 表示跳过调用但返回成功，并不执行调用。`KILL_PROCESS` 终止整个进程；首版不支持只杀当前线程的动作，避免 Go 初始化进程只剩部分线程继续运行。

参数按内核传入的原始数值比较。`MASKED_EQ` 表示 `(参数 & value) == valueTwo`，其中 value 是掩码。非原生 ABI 会触发终止进程的动作。

配置校验检查字段和支持范围；系统调用名称是否能解析、规则能否编译，以及宿主能否安装过滤器，在 Linux 初始化阶段检查。不能把 `validate` 成功当作内核已接受策略。

## 安装时机与权限

初始化先完成 namespace、rootfs、资源限制、身份、capabilities、no_new_privs 和工作目录，再安装 seccomp。过滤器安装后不能撤销，后续创建的子进程和 exec 后的程序会继承它。

安装过滤器通常要求已设置 `no_new_privs`，或者在当前 user namespace 中具有 `CAP_SYS_ADMIN`。minirunc 禁止 libseccomp 自动开启 no_new_privs：OCI 配置为 false 且权限不足时必须失败，不能偷偷修改配置。

Go 初始化进程有多个 OS 线程。libseccomp 创建过滤器时请求 TSYNC，把过滤器同步到其他线程；minirunc 要求 libseccomp API 等级至少为 3，以支持线程同步和终止整个进程。初始化 goroutine 仍需锁定线程，保证前面的身份与能力操作顺序不变。

## 启动通信也受过滤

过滤器生效后，初始化进程仍需释放编译资源、写入最终结果并退出；以后还需要 exec 用户程序。默认拒绝策略必须考虑这些调用以及 Go 运行时需要的调用，不能只允许用户程序的业务调用。

minirunc 不额外放行 write、close、exit_group 等调用，也不修改用户策略。安装前先发送阶段标记，安装后才发送成功结果；缺少最终确认不能视为成功。父进程负责回收子进程并清理 cgroup，终止信号可用于诊断；阻塞场景仍需要调用方通过 context 取消。

当前验证到初始化完成，尚未执行容器用户程序，因此还不能声称某份策略已通过完整的 exec 与程序运行验证。

参考：[OCI Linux seccomp 配置](https://github.com/opencontainers/runtime-spec/blob/v1.3.0/config-linux.md#seccomp)、[Linux seccomp 过滤器](https://www.kernel.org/doc/html/latest/userspace-api/seccomp_filter.html)、[libseccomp Go 绑定](https://github.com/seccomp/libseccomp-golang)。
