package container

import (
	"fmt"
	"os"
	"runtime"

	"github.com/lulaide/minirunc/internal/linux"
	"golang.org/x/sys/unix"
)

const (
	initCommand   = "__init"
	configFD      = 3
	resultFD      = 4
	parentMountFD = 5
	parentUTSFD   = 6
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
	// 将初始化 goroutine 固定在当前 OS 线程，依赖线程状态的操作保持一致。
	// namespace 已在启动子进程时创建；锁定线程本身不会创建隔离或停止其他线程。
	// 内部入口最终会退出整个进程，不把这个线程交回普通 CLI 使用。
	runtime.LockOSThread()
	// NewFile 不会创建或打开管道，而是把继承的 fd 包装为 *os.File。
	// 它实现 io.Reader/io.Writer，因此可以交给上一部分的协议函数使用。
	configRead := os.NewFile(configFD, "init-config")
	resultWrite := os.NewFile(resultFD, "init-result")
	parentMount := os.NewFile(parentMountFD, "parent-mount-namespace")
	parentUTS := os.NewFile(parentUTSFD, "parent-uts-namespace")
	defer configRead.Close()
	defer resultWrite.Close()
	defer parentMount.Close()
	defer parentUTS.Close()

	config, err := readInitConfig(configRead)
	if err == nil {
		err = setupInit(config, parentMount, parentUTS)
	}
	if err != nil {
		// 错误通过结果管道交给父进程，不在子进程重复打印日志。
		_ = writeInitMessage(resultWrite, initResult{Error: err.Error()})
		return 1
	}

	// 只有 hostname 和 rootfs 初始化完成后才确认成功。
	// 当前入口随即退出，尚未等待 start 或 exec 用户程序，因此不是 OCI running。
	if err := writeInitMessage(resultWrite, initResult{Ready: true}); err != nil {
		return 1
	}
	// 返回后 defer 关闭结果写端，父进程才能读到 EOF 并解析结果。
	return 0
}

func setupInit(config *initConfig, parentMount, parentUTS *os.File) error {
	if _, err := initCloneFlags(config); err != nil {
		return err
	}
	// 再核对实际 namespace，避免仅凭配置声明就修改宿主挂载或 hostname。
	// 即使直接调用 __init 并传入合法配置，也必须满足隔离前提。
	if err := requireNewNamespace("mnt", unix.CLONE_NEWNS, parentMount); err != nil {
		return err
	}
	if config.Spec.Hostname != "" {
		if err := requireNewNamespace("uts", unix.CLONE_NEWUTS, parentUTS); err != nil {
			return err
		}
	}
	// 比对完成后不再需要宿主 namespace 句柄，及时关闭，避免后续继承。
	_ = parentMount.Close()
	_ = parentUTS.Close()
	if config.Spec.Hostname != "" {
		if err := unix.Sethostname([]byte(config.Spec.Hostname)); err != nil {
			return fmt.Errorf("set hostname: %w", err)
		}
	}
	return linux.SetupRootfs(linux.RootfsConfig{
		Path:          config.RootfsPath,
		Readonly:      config.Spec.Root.Readonly,
		Mounts:        config.Spec.Mounts,
		MaskedPaths:   config.Spec.Linux.MaskedPaths,
		ReadonlyPaths: config.Spec.Linux.ReadonlyPaths,
	})
}

func requireNewNamespace(name string, namespaceType int, parent *os.File) error {
	// NS_GET_NSTYPE 查询句柄指向的 namespace 类型；普通文件或错误类型
	// 不能作为比对依据，否则两个不同文件也会被误判为已经隔离。
	typeFlag, err := unix.IoctlRetInt(int(parent.Fd()), unix.NS_GET_NSTYPE)
	if err != nil {
		return fmt.Errorf("inspect parent %s namespace: %w", name, err)
	}
	if typeFlag != namespaceType {
		return fmt.Errorf("parent %s namespace: unexpected namespace type", name)
	}
	parentInfo, err := parent.Stat()
	if err != nil {
		return fmt.Errorf("stat parent %s namespace: %w", name, err)
	}
	currentInfo, err := os.Stat("/proc/self/ns/" + name)
	if err != nil {
		return fmt.Errorf("stat current %s namespace: %w", name, err)
	}
	// 同一 namespace 对象的设备号和 inode 相同，文件路径或 fd 数值不够可靠。
	if os.SameFile(parentInfo, currentInfo) {
		return fmt.Errorf("init process must be in a new %s namespace", name)
	}
	return nil
}
