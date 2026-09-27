package container

import (
	"os"

	"github.com/lulaide/minirunc/internal/spec"
)

const (
	initCommand = "__init"
	configFD    = 3
	resultFD    = 4
)

// HandleInit 在 CLI 解析之前识别内部入口，供 re-exec 子进程使用。
// __init 不注册为公开子命令；普通 CLI 调用由调用方继续处理。
func HandleInit(args []string) (handled bool, exitCode int) {
	if len(args) != 1 || args[0] != initCommand {
		return false, 0
	}
	return true, runInit()
}

func runInit() int {
	// NewFile 不会创建或打开管道，而是把继承的 fd 包装为 *os.File。
	// 它实现 io.Reader/io.Writer，因此可以交给上一部分的协议函数使用。
	configRead := os.NewFile(configFD, "init-config")
	resultWrite := os.NewFile(resultFD, "init-result")
	defer configRead.Close()
	defer resultWrite.Close()

	config, err := readInitConfig(configRead)
	if err == nil {
		err = spec.ValidateSpec(config.Spec)
	}
	if err != nil {
		// 错误通过结果管道交给父进程，不在子进程重复打印日志。
		_ = writeInitMessage(resultWrite, initResult{Error: err.Error()})
		return 1
	}

	// 当前只确认配置接收和基础校验成功，不能据此宣告容器隔离已完成。
	// 接入实际初始化操作后，成功结果必须在那些操作完成后才能发送。
	if err := writeInitMessage(resultWrite, initResult{Ready: true}); err != nil {
		return 1
	}
	// 返回后 defer 关闭结果写端，父进程才能读到 EOF 并解析结果。
	return 0
}
