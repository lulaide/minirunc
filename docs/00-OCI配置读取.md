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

## 参考

- [OCI bundle 定义](https://github.com/opencontainers/runtime-spec/blob/v1.3.0/bundle.md)
- [OCI 配置说明](https://github.com/opencontainers/runtime-spec/blob/v1.3.0/config.md)
- [specs-go 类型定义](https://github.com/opencontainers/runtime-spec/blob/v1.3.0/specs-go/config.go)
