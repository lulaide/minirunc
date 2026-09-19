package linux

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

const oldRootDir = ".minirunc-oldroot"

type RootfsConfig struct {
	Path          string
	Readonly      bool
	Mounts        []specs.Mount
	MaskedPaths   []string
	ReadonlyPaths []string
}

// SetupRootfs 将调用进程切换到 rootfs。调用方必须已经位于独立的 mount
// namespace 中。成功后可以继续设置进程属性并 execve 用户程序；如果中途
// 失败，挂载状态可能只完成了一部分，调用方必须立即终止当前初始化进程。
func SetupRootfs(config RootfsConfig) error {
	if !filepath.IsAbs(config.Path) {
		return fmt.Errorf("rootfs path %q: must be absolute", config.Path)
	}
	info, err := os.Stat(config.Path)
	if err != nil {
		return fmt.Errorf("stat rootfs: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("rootfs path %q: not a directory", config.Path)
	}
	// 新的 mount namespace 最初会复制父进程的挂载表。如果这些挂载仍保持
	// shared 传播关系，后面在容器中进行的 mount/umount 可能传播回宿主。
	// source 和 filesystem type 留空，表示这里只修改已有挂载的传播属性；
	// MS_REC 让设置递归覆盖 / 下的挂载，MS_PRIVATE 关闭双向传播。
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make root mount private: %w", err)
	}

	// rootfs 此时只是宿主文件系统中的普通目录。pivot_root 要求 new_root
	// 自己是一个挂载点，不能直接使用普通目录。把目录 bind mount 到它自己
	// 不会复制文件，也不会改变路径中的内容；它只是在内核挂载表中为同一棵
	// 目录树增加一个独立挂载记录，使 rootfs 成为合法的 new_root。
	if err := unix.Mount(config.Path, config.Path, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("bind mount rootfs: %w", err)
	}

	// 从这里开始，当前进程看到的 / 会变成 rootfs。旧的宿主根在
	// pivotRoot 返回前已经卸载，后续绝对路径只能在容器根中解析。
	if err := pivotRoot(config.Path); err != nil {
		return err
	}

	// OCI mounts 按数组顺序执行。例如必须先挂载 /dev，再挂载 /dev/pts；
	// 顺序颠倒时，后挂载的 /dev 会遮住已经存在的 /dev/pts。
	for i, mount := range config.Mounts {
		if err := mountFilesystem(mount); err != nil {
			return fmt.Errorf("mounts[%d] at %q: %w", i, mount.Destination, err)
		}
	}
	// /dev 通常是新挂载的空 tmpfs，需要补充 Linux 程序普遍依赖的基础设备。
	if err := createDefaultDevices(); err != nil {
		return err
	}

	// 普通挂载完成后再覆盖敏感路径，否则后续的 /proc 或 /sys 挂载会把
	// 已经设置好的屏蔽和只读挂载遮住。
	for i, path := range config.MaskedPaths {
		if err := maskPath(path); err != nil {
			return fmt.Errorf("maskedPaths[%d] %q: %w", i, path, err)
		}
	}
	for i, path := range config.ReadonlyPaths {
		if err := makeReadonly(path); err != nil {
			return fmt.Errorf("readonlyPaths[%d] %q: %w", i, path, err)
		}
	}
	if config.Readonly {
		if err := remountReadonly("/"); err != nil {
			return fmt.Errorf("make rootfs readonly: %w", err)
		}
	}
	return nil
}

func pivotRoot(rootfs string) error {
	// pivot_root 不会直接丢弃旧根，而是要求 new_root 内提供 put_old 目录。
	// 系统调用完成后，旧的宿主根会暂时出现在 /.minirunc-oldroot。
	putOld := filepath.Join(rootfs, oldRootDir)
	if err := os.Mkdir(putOld, 0700); err != nil {
		return fmt.Errorf("create old root directory: %w", err)
	}
	pivoted := false
	defer func() {
		if !pivoted {
			_ = os.Remove(putOld)
		}
	}()
	// 使用相对路径调用 pivot_root，确保 new_root 和 put_old 的关系清楚：
	// 当前目录 . 是新根，.minirunc-oldroot 是新根内部用于接收旧根的目录。
	if err := os.Chdir(rootfs); err != nil {
		return fmt.Errorf("change directory to rootfs: %w", err)
	}
	if err := unix.PivotRoot(".", oldRootDir); err != nil {
		return fmt.Errorf("pivot root: %w", err)
	}
	pivoted = true

	// pivot_root 只替换根挂载，不会自动修改进程的当前工作目录。显式切换
	// 到新的 /，避免 cwd 继续引用切换前的目录树并阻止旧根卸载。
	if err := os.Chdir("/"); err != nil {
		return fmt.Errorf("change directory to new root: %w", err)
	}
	oldRoot := "/" + oldRootDir

	// 只执行 pivot_root 还不构成边界：进程仍能通过 put_old 访问整个宿主。
	// MNT_DETACH 立即从当前挂载树摘除旧根；如果内核中仍有临时引用，等
	// 引用释放后再完成清理。卸载成功后删除空的 put_old 目录。
	if err := unix.Unmount(oldRoot, unix.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount old root: %w", err)
	}
	if err := os.Remove(oldRoot); err != nil {
		return fmt.Errorf("remove old root directory: %w", err)
	}
	return nil
}

func mountFilesystem(mount specs.Mount) error {
	switch mount.Type {
	case "proc", "tmpfs", "devpts", "mqueue", "sysfs":
	default:
		return fmt.Errorf("filesystem type %q is not supported", mount.Type)
	}
	if err := ensureDirectory(mount.Destination); err != nil {
		return err
	}
	flags, propagation, data, err := parseMountOptions(mount.Options)
	if err != nil {
		return err
	}
	// 前面的 pivot_root 已经完成，因此 destination 是容器内的绝对路径，
	// 即使 rootfs 中包含符号链接，也不能再沿路径访问已经卸载的宿主根。
	if err := unix.Mount(mount.Source, mount.Destination, mount.Type, flags, data); err != nil {
		return fmt.Errorf("mount %s: %w", mount.Type, err)
	}
	if propagation != 0 {
		if err := unix.Mount("", mount.Destination, "", propagation, ""); err != nil {
			return fmt.Errorf("set mount propagation: %w", err)
		}
	}
	return nil
}

func ensureDirectory(path string) error {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return errors.New("destination must be an absolute path without NUL")
	}
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return errors.New("destination is not a directory")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat destination: %w", err)
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	return nil
}

func parseMountOptions(options []string) (flags uintptr, propagation uintptr, data string, err error) {
	// OCI 把所有 mount options 表示成字符串，但 mount(2) 需要拆成三部分：
	// 1. ro、nosuid 等通用语义转换为 flags；
	// 2. private、shared 等传播方式在首次 mount 后单独设置；
	// 3. mode=755、size=64m 等由具体文件系统解释的选项拼成 data。
	var filesystemOptions []string
	for _, option := range options {
		switch option {
		case "defaults":
		case "ro":
			flags |= unix.MS_RDONLY
		case "rw":
			flags &^= unix.MS_RDONLY
		case "nosuid":
			flags |= unix.MS_NOSUID
		case "suid":
			flags &^= unix.MS_NOSUID
		case "nodev":
			flags |= unix.MS_NODEV
		case "dev":
			flags &^= unix.MS_NODEV
		case "noexec":
			flags |= unix.MS_NOEXEC
		case "exec":
			flags &^= unix.MS_NOEXEC
		case "sync":
			flags |= unix.MS_SYNCHRONOUS
		case "async":
			flags &^= unix.MS_SYNCHRONOUS
		case "dirsync":
			flags |= unix.MS_DIRSYNC
		case "mand":
			flags |= unix.MS_MANDLOCK
		case "nomand":
			flags &^= unix.MS_MANDLOCK
		case "noatime":
			flags |= unix.MS_NOATIME
		case "atime":
			flags &^= unix.MS_NOATIME
		case "nodiratime":
			flags |= unix.MS_NODIRATIME
		case "diratime":
			flags &^= unix.MS_NODIRATIME
		case "relatime":
			flags = flags&^unix.MS_STRICTATIME | unix.MS_RELATIME
		case "norelatime":
			flags &^= unix.MS_RELATIME
		case "strictatime":
			flags = flags&^unix.MS_RELATIME | unix.MS_STRICTATIME
		case "nostrictatime":
			flags &^= unix.MS_STRICTATIME
		case "lazytime":
			flags |= unix.MS_LAZYTIME
		case "nolazytime":
			flags &^= unix.MS_LAZYTIME
		case "silent":
			flags |= unix.MS_SILENT
		case "loud":
			flags &^= unix.MS_SILENT
		case "bind", "rbind", "remount", "rec":
			return 0, 0, "", fmt.Errorf("mount option %q is not supported", option)
		case "private", "rprivate", "shared", "rshared", "slave", "rslave", "unbindable", "runbindable":
			value, recursive := strings.TrimPrefix(option, "r"), strings.HasPrefix(option, "r")
			var next uintptr
			switch value {
			case "private":
				next = unix.MS_PRIVATE
			case "shared":
				next = unix.MS_SHARED
			case "slave":
				next = unix.MS_SLAVE
			case "unbindable":
				next = unix.MS_UNBINDABLE
			}
			if recursive {
				next |= unix.MS_REC
			}
			if propagation != 0 {
				return 0, 0, "", errors.New("multiple mount propagation options are not supported")
			}
			propagation = next
		default:
			if strings.ContainsRune(option, '\x00') {
				return 0, 0, "", errors.New("mount option must not contain NUL")
			}
			filesystemOptions = append(filesystemOptions, option)
		}
	}
	return flags, propagation, strings.Join(filesystemOptions, ","), nil
}

type device struct {
	path       string
	major      uint32
	minor      uint32
	permission uint32
}

func createDefaultDevices() error {
	devices := []device{
		{"/dev/null", 1, 3, 0666},
		{"/dev/zero", 1, 5, 0666},
		{"/dev/full", 1, 7, 0666},
		{"/dev/random", 1, 8, 0666},
		{"/dev/urandom", 1, 9, 0666},
		{"/dev/tty", 5, 0, 0666},
	}
	for _, device := range devices {
		if err := createDevice(device); err != nil {
			return fmt.Errorf("create device %s: %w", device.path, err)
		}
	}
	links := map[string]string{
		"/dev/fd":     "/proc/self/fd",
		"/dev/stdin":  "/proc/self/fd/0",
		"/dev/stdout": "/proc/self/fd/1",
		"/dev/stderr": "/proc/self/fd/2",
		"/dev/ptmx":   "pts/ptmx",
	}
	for path, target := range links {
		if err := ensureSymlink(target, path); err != nil {
			return fmt.Errorf("create symlink %s: %w", path, err)
		}
	}
	return nil
}

func createDevice(device device) error {
	var stat unix.Stat_t
	err := unix.Lstat(device.path, &stat)
	if err == nil {
		if stat.Mode&unix.S_IFMT != unix.S_IFCHR ||
			unix.Major(uint64(stat.Rdev)) != device.major ||
			unix.Minor(uint64(stat.Rdev)) != device.minor {
			return errors.New("existing path is not the requested character device")
		}
		return unix.Chmod(device.path, device.permission)
	}
	if !errors.Is(err, unix.ENOENT) {
		return err
	}
	return unix.Mknod(device.path, unix.S_IFCHR|device.permission, int(unix.Mkdev(device.major, device.minor)))
}

func ensureSymlink(target, path string) error {
	existing, err := os.Readlink(path)
	if err == nil {
		if existing != target {
			return fmt.Errorf("existing symlink points to %q", existing)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(target, path)
}

func maskPath(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		// 用空且只读的 tmpfs 覆盖目录，容器既看不到原内容也无法写入。
		return unix.Mount("tmpfs", path, "tmpfs", unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "mode=000")
	}
	// 文件不能挂载 tmpfs 目录，因此把 /dev/null bind mount 到该文件上。
	return unix.Mount("/dev/null", path, "", unix.MS_BIND, "")
}

func makeReadonly(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	// 只读是挂载点属性，不是普通目录属性。先把路径 bind mount 到自身，
	// 为它创建独立挂载点，再 remount 为只读，避免修改整个底层文件系统。
	if err := unix.Mount(path, path, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("bind mount path: %w", err)
	}
	return remountReadonly(path)
}

func remountReadonly(path string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return fmt.Errorf("stat filesystem: %w", err)
	}
	// remount 时内核要求保留原挂载的一些安全和 atime flags，否则可能在
	// 设置只读的同时意外取消 nosuid、nodev 或 noexec。
	preserve := uintptr(stat.Flags) & (unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC |
		unix.MS_NOATIME | unix.MS_NODIRATIME | unix.MS_RELATIME | unix.MS_LAZYTIME)
	if err := unix.Mount("", path, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY|preserve, ""); err != nil {
		return fmt.Errorf("remount readonly: %w", err)
	}
	return nil
}
