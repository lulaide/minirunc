# 00 - OCI 配置读取

## OCI bundle

OCI bundle 是容器运行时使用的一组本地文件，主要包含 bundle 根目录下的 `config.json` 和配置所引用的根文件系统。

`config.json` 描述容器如何运行；根文件系统提供程序、依赖库和目录等文件，通常放在 `rootfs/` 中。实际位置由 `root.path` 决定，它可以是宿主绝对路径，也可以是相对于 bundle 的路径。

镜像需要经过解包等准备工作才能供运行时使用。minirunc 接收准备好的 bundle，镜像下载和存储管理由上层负责。

## config.json

常见配置包括：

| 字段 | 含义 |
| --- | --- |
| `ociVersion` | 配置遵循的 OCI Runtime Spec 版本 |
| `root` | 根文件系统的位置和只读属性 |
| `process` | 启动参数、环境变量、工作目录、用户及进程安全设置 |
| `mounts` | 额外挂载的来源、目标、类型和选项 |
| `hostname` | 容器主机名 |
| `linux` | namespace、cgroup、设备、seccomp 等 Linux 配置 |
| `hooks`、`annotations` | 生命周期钩子和附加元数据 |

容器 ID、宿主 PID 和运行状态属于运行时管理数据，不是这份配置的内容。

## specs-go

`github.com/opencontainers/runtime-spec/specs-go` 是 OCI 官方提供的 Go 类型定义，包名为 `specs`。其中 `specs.Spec` 对应完整配置，`Process`、`Root`、`Linux` 等类型对应各部分。

这些类型带有 JSON 标签，可以用标准库 `encoding/json` 将配置解码为 `specs.Spec`。它提供数据结构，配置校验和实际的容器操作仍由运行时负责。

## 解码与校验

- 解码检查 JSON 能否转换为 Go 对象，例如语法和字段类型是否正确。
- 校验检查配置是否符合规范及运行时支持范围，例如必填字段、路径要求和 namespace 配置是否有效。

解码成功不等于配置可以运行。例如 `{}` 可以解码为 `specs.Spec`，但缺少运行 Linux 容器需要的根文件系统配置。

## rootfs 路径

rootfs 是容器进程看到的根文件系统，包含程序、动态链接器、依赖库和系统目录。OCI bundle 通常将它放在 `rootfs/`，实际位置由 `root.path` 指定。

在 POSIX 平台上，`root.path` 可以是绝对路径，也可以是相对于 bundle 的路径。例如 bundle 位于 `/run/example` 时：

| 配置值 | 宿主侧 rootfs 路径 |
| --- | --- |
| `rootfs` | `/run/example/rootfs` |
| `../rootfs` | `/run/rootfs` |
| `/var/lib/rootfs` | `/var/lib/rootfs` |

运行时分别保留配置中的 `root.path` 和解析后的宿主绝对路径。前者表达原始 OCI 配置，后者供文件检查和后续挂载使用。

`root.readonly` 表示根文件系统在容器中是否只读，不改变调用方在宿主侧提供的文件。

加载阶段可以检查 rootfs 是否存在并且是目录，但这只是早期错误检查。检查与实际挂载之间，路径内容仍可能变化，符号链接也可能指向其他位置。后续挂载实现仍需在正确的 mount namespace 中安全打开和解析路径。

## 参考

- [OCI bundle 定义](https://github.com/opencontainers/runtime-spec/blob/v1.3.0/bundle.md)
- [OCI 配置说明](https://github.com/opencontainers/runtime-spec/blob/v1.3.0/config.md)
- [specs-go 类型定义](https://github.com/opencontainers/runtime-spec/blob/v1.3.0/specs-go/config.go)
