package spec

import (
	"errors"
	"fmt"
	"path"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const OCIVersion = "1.3.0"

// ValidateSpec 检查 Linux 容器初始化进程所需的基础配置。
// 它不检查所有 OCI 字段，也不判断 runtime 是否已经具备相应执行能力。
func ValidateSpec(config *specs.Spec) error {
	if config == nil {
		return errors.New("config: field is required")
	}
	var problems []error
	if config.Version != OCIVersion {
		problems = append(problems, fmt.Errorf("ociVersion: expected %q, got %q", OCIVersion, config.Version))
	}
	if config.Windows != nil || config.Solaris != nil || config.FreeBSD != nil || config.VM != nil || config.ZOS != nil {
		problems = append(problems, errors.New("config: non-Linux platform configuration is not supported"))
	}
	if config.Root == nil || config.Root.Path == "" {
		problems = append(problems, errors.New("root.path: field is required"))
	} else if strings.ContainsRune(config.Root.Path, '\x00') {
		problems = append(problems, errors.New("root.path: must not contain NUL"))
	}
	problems = append(problems, validateProcess(config.Process)...)
	for i, mount := range config.Mounts {
		if !absolutePath(mount.Destination) {
			problems = append(problems, fmt.Errorf("mounts[%d].destination: must be an absolute path without NUL", i))
		}
	}
	if config.Linux != nil {
		problems = append(problems, validateNamespaces(config.Linux.Namespaces)...)
		for i, value := range config.Linux.MaskedPaths {
			if !absolutePath(value) {
				problems = append(problems, fmt.Errorf("linux.maskedPaths[%d]: must be an absolute path without NUL", i))
			}
		}
		for i, value := range config.Linux.ReadonlyPaths {
			if !absolutePath(value) {
				problems = append(problems, fmt.Errorf("linux.readonlyPaths[%d]: must be an absolute path without NUL", i))
			}
		}
	}
	return errors.Join(problems...)
}

func validateProcess(process *specs.Process) []error {
	if process == nil {
		return []error{errors.New("process: field is required for starting a container")}
	}
	var problems []error
	if len(process.Args) == 0 || process.Args[0] == "" {
		problems = append(problems, errors.New("process.args: executable is required"))
	}
	for i, arg := range process.Args {
		if strings.ContainsRune(arg, '\x00') {
			problems = append(problems, fmt.Errorf("process.args[%d]: must not contain NUL", i))
		}
	}
	if !absolutePath(process.Cwd) {
		problems = append(problems, errors.New("process.cwd: must be an absolute path without NUL"))
	}
	for i, env := range process.Env {
		name, _, ok := strings.Cut(env, "=")
		if !ok || name == "" || strings.ContainsRune(env, '\x00') {
			problems = append(problems, fmt.Errorf("process.env[%d]: expected NAME=VALUE without NUL", i))
		}
	}
	if process.CommandLine != "" || process.User.Username != "" {
		problems = append(problems, errors.New("process: commandLine and user.username are Windows-only fields"))
	}
	return problems
}

func validateNamespaces(namespaces []specs.LinuxNamespace) []error {
	var problems []error
	seen := make(map[specs.LinuxNamespaceType]bool)
	for i, namespace := range namespaces {
		switch namespace.Type {
		case specs.PIDNamespace, specs.NetworkNamespace, specs.MountNamespace,
			specs.IPCNamespace, specs.UTSNamespace, specs.UserNamespace,
			specs.CgroupNamespace, specs.TimeNamespace:
		default:
			problems = append(problems, fmt.Errorf("linux.namespaces[%d].type: unknown type %q", i, namespace.Type))
		}
		if seen[namespace.Type] {
			problems = append(problems, fmt.Errorf("linux.namespaces[%d].type: duplicate type %q", i, namespace.Type))
		}
		seen[namespace.Type] = true
		if namespace.Path != "" && !absolutePath(namespace.Path) {
			problems = append(problems, fmt.Errorf("linux.namespaces[%d].path: must be an absolute path without NUL", i))
		}
	}
	return problems
}

func absolutePath(value string) bool {
	return path.IsAbs(value) && !strings.ContainsRune(value, '\x00')
}
