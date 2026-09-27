package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// startInit 重新执行指定的 minirunc 可执行文件，并完成初始化通信。
// 正式启动时 executable 使用 /proc/self/exe，表示当前进程运行的程序。
// 当前子进程只接收并检查配置，尚未设置 namespace、rootfs 或执行用户程序。
func startInit(ctx context.Context, executable string, config *initConfig) error {
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
	// ExtraFiles 按顺序映射到子进程的 fd 3、fd 4；父进程中的 fd 数值
	// 可能不同，不能把父进程的数值直接告诉子进程使用。
	child := exec.CommandContext(ctx, executable, initCommand)
	child.ExtraFiles = []*os.File{configRead, resultWrite}
	child.Env = []string{}
	if err := child.Start(); err != nil {
		return fmt.Errorf("start init process: %w", err)
	}

	// Start 后父进程关闭自己不使用的端点。特别是结果写端：如果父进程
	// 留着它，即使子进程关闭写端，父进程的结果读取也无法得到 EOF。
	_ = configRead.Close()
	_ = resultWrite.Close()

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
