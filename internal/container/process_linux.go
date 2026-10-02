package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/lulaide/minirunc/internal/linux"
	"github.com/lulaide/minirunc/internal/spec"
	"golang.org/x/sys/unix"
)

// startInit 重新执行指定的 minirunc 可执行文件，并完成初始化通信。
// 正式启动时 executable 使用 /proc/self/exe，表示当前进程运行的程序。
// 当前子进程完成 namespace、hostname 和 rootfs 初始化后退出，尚不执行用户程序。
func startInit(ctx context.Context, executable string, config *initConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	flags, err := initCloneFlags(config)
	if err != nil {
		return err
	}
	// 打开 namespace 文件得到的是内核 namespace 对象的句柄，不是目录。
	// 子进程用它们与自身的 namespace 比对，确认隔离确实已经建立。
	parentMount, err := os.Open("/proc/self/ns/mnt")
	if err != nil {
		return fmt.Errorf("open parent mount namespace: %w", err)
	}
	defer parentMount.Close()
	parentUTS, err := os.Open("/proc/self/ns/uts")
	if err != nil {
		return fmt.Errorf("open parent UTS namespace: %w", err)
	}
	defer parentUTS.Close()

	configRead, configWrite, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create config pipe: %w", err)
	}
	defer configRead.Close()
	defer configWrite.Close()

	resultRead, resultWrite, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create result pipe: %w", err)
	}
	defer resultRead.Close()
	defer resultWrite.Close()

	// os.Pipe 返回的是内核管道的两个文件描述符，没有文件系统路径。
	// ExtraFiles 按顺序映射到子进程：fd 3 读配置，fd 4 写结果，
	// fd 5、fd 6 分别引用父进程的 mount、UTS namespace。
	// 父进程中的 fd 数值可能不同，不能直接告诉子进程使用。
	child := exec.CommandContext(ctx, executable, initCommand)
	child.ExtraFiles = []*os.File{configRead, resultWrite, parentMount, parentUTS}
	// Go 在执行子程序、启动其 Go runtime 之前创建这些 namespace。
	// 父进程留在原来的 namespace 中，不直接执行 mount 或 pivot_root。
	child.SysProcAttr = &syscall.SysProcAttr{Cloneflags: flags}
	child.Env = []string{}
	if err := child.Start(); err != nil {
		return fmt.Errorf("start init process: %w", err)
	}

	// Start 后父进程关闭自己不使用的端点。特别是结果写端：如果父进程
	// 留着它，即使子进程关闭写端，父进程的结果读取也无法得到 EOF。
	_ = configRead.Close()
	_ = resultWrite.Close()
	_ = parentMount.Close()
	_ = parentUTS.Close()

	// 每个成功启动的子进程都必须 Wait，否则退出后可能残留僵尸进程。
	// 中途通信失败时先终止子进程，再 Wait，避免它仍阻塞在管道读取上。
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()

	if err := writeInitMessage(configWrite, config); err != nil {
		return err
	}
	// 子进程用 ReadAll 接收配置，必须关闭写端才能让它开始解析消息。
	if err := configWrite.Close(); err != nil {
		return fmt.Errorf("close config pipe: %w", err)
	}
	result, err := readInitResult(resultRead)
	if err != nil {
		return fmt.Errorf("receive init result: %w", err)
	}
	waitErr := child.Wait()
	waited = true
	if result.Error != "" {
		return fmt.Errorf("init process failed: %s", result.Error)
	}
	if waitErr != nil {
		return fmt.Errorf("wait for init process: %w", waitErr)
	}
	return nil
}

func initCloneFlags(config *initConfig) (uintptr, error) {
	if err := validateInitConfig(config); err != nil {
		return 0, err
	}
	if err := spec.ValidateSpec(config.Spec); err != nil {
		return 0, err
	}
	if config.Spec.Linux == nil {
		return 0, fmt.Errorf("linux: configuration is required for isolation setup")
	}
	flags, err := linux.NamespaceCloneFlags(config.Spec.Linux.Namespaces)
	if err != nil {
		return 0, err
	}
	// SetupRootfs 会修改当前挂载表，必须先创建独立的 mount namespace。
	// hostname 属于 UTS namespace；缺少隔离时设置它会影响宿主。
	if flags&unix.CLONE_NEWNS == 0 {
		return 0, fmt.Errorf("linux.namespaces: a new mount namespace is required for rootfs setup")
	}
	if config.Spec.Hostname != "" && flags&unix.CLONE_NEWUTS == 0 {
		return 0, fmt.Errorf("linux.namespaces: a new UTS namespace is required for hostname")
	}
	return flags, nil
}
