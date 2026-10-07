package cgroup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

// Group 只管理自己创建的一个叶子 cgroup，不接管已有目录。
// 父目录由 runtime 的调用方指定，必须是明确授权管理的 cgroup v2 子树。
type Group struct {
	parent    *os.File
	directory *os.File
	name      string
}

// Create 在父目录下启用所需控制器、创建新 cgroup 并设置初始限制。
// name 当前只支持一个目录名，不接受 OCI 绝对路径、多级路径或 systemd scope 格式。
func Create(parentPath, name string, resources *specs.LinuxResources) (*Group, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	settings, err := resourceSettings(resources)
	if err != nil {
		return nil, err
	}
	parent, err := openDirectory(parentPath)
	if err != nil {
		return nil, err
	}
	if err := enableControllers(parent, settings); err != nil {
		_ = parent.Close()
		return nil, err
	}
	// 不使用 MkdirAll，也不复用 EEXIST 的目录，避免修改或删除其他容器的 cgroup。
	if err := unix.Mkdirat(int(parent.Fd()), name, 0755); err != nil {
		_ = parent.Close()
		return nil, fmt.Errorf("create cgroup %q: %w", name, err)
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		removeErr := unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
		_ = parent.Close()
		return nil, errors.Join(fmt.Errorf("open new cgroup: %w", err), removeErr)
	}
	group := &Group{parent: parent, directory: os.NewFile(uintptr(fd), name), name: name}
	if err := applySettings(group.directory, settings); err != nil {
		return nil, errors.Join(err, group.Remove(), group.Close())
	}
	return group, nil
}

func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/:\x00") {
		return fmt.Errorf("linux.cgroupsPath: expected a single cgroup directory name")
	}
	return nil
}

// AddPID 将宿主视角的进程 PID 写入 cgroup.procs；内核会迁移该进程的所有线程。
// 容器内的 PID 1 不能用在这里。后续 fork/clone 创建的任务会继承所属 cgroup。
func (group *Group) AddPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("cgroup PID must be positive")
	}
	return writeControl(group.directory, "cgroup.procs", strconv.Itoa(pid))
}

// Remove 只删除本组的目录。仍有任务或子 cgroup 时内核会拒绝，不能递归删除控制文件。
// 调用者必须先终止并回收进程；删除失败时保留错误供上层诊断。
func (group *Group) Remove() error {
	if err := unix.Unlinkat(int(group.parent.Fd()), group.name, unix.AT_REMOVEDIR); err != nil {
		return fmt.Errorf("remove cgroup %q: %w", group.name, err)
	}
	return nil
}

// Close 释放 runtime 持有的目录句柄，与删除内核中的 cgroup 是两个不同操作。
func (group *Group) Close() error {
	return errors.Join(group.directory.Close(), group.parent.Close())
}

func enableControllers(parent *os.File, settings []limitSetting) error {
	if len(settings) == 0 {
		return nil
	}
	available, err := readControl(parent, "cgroup.controllers")
	if err != nil {
		return err
	}
	enabled, err := readControl(parent, "cgroup.subtree_control")
	if err != nil {
		return err
	}
	var commands []string
	for _, setting := range settings {
		controller, _, _ := strings.Cut(setting.file, ".")
		if !containsWord(available, controller) {
			return fmt.Errorf("cgroup controller %q is not available in parent", controller)
		}
		if !containsWord(enabled, controller) {
			commands = append(commands, "+"+controller)
		}
	}
	if len(commands) == 0 {
		return nil
	}
	// 域控制器要求父组没有直属进程；不迁移宿主进程去绕过这个约束。
	// 控制器启用属于父树准备，不在叶子删除时关闭，避免影响其他子组。
	if err := writeControl(parent, "cgroup.subtree_control", strings.Join(commands, " ")); err != nil {
		return fmt.Errorf("enable cgroup controllers (parent must allow delegation and have no direct processes): %w", err)
	}
	return nil
}

func containsWord(text, word string) bool {
	for _, value := range strings.Fields(text) {
		if value == word {
			return true
		}
	}
	return false
}

func readControl(directory *os.File, name string) (string, error) {
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", fmt.Errorf("open cgroup control %s: %w", name, err)
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read cgroup control %s: %w", name, err)
	}
	return string(data), nil
}
