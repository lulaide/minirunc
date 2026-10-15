# minirunc

minirunc 是一个使用 Go 实现的精简 Linux 容器运行时，通过写这个项目加深对 Linux、容器的认识。职责对齐 runc，面向 OCI Runtime Spec 设计。

项目围绕同一条容器生命周期逐步实现 namespace、rootfs 与挂载管理、cgroup v2、信号与进程回收，以及 capabilities、`no_new_privs`、seccomp 等安全机制。

## 项目结构

```text
minirunc/
├── main.go                         进程入口
├── internal/
│   ├── command/                    CLI 参数、命令分发、日志与错误输出
│   │   ├── root.go
│   │   └── validate.go
│   ├── spec/                       OCI bundle 读取、校验与配置转换
│   │   ├── load.go
│   │   ├── validate.go
│   │   └── capabilities.go
│   ├── container/                  组织初始化流程与父子进程通信
│   │   ├── process_linux.go         父进程：启动、加入 cgroup、等待与清理
│   │   ├── init_linux.go            子进程：隔离环境、身份与权限初始化
│   │   └── protocol.go              配置与结果消息
│   ├── linux/                      Linux 底层操作
│   │   ├── namespace_linux.go       namespace 创建参数
│   │   ├── rootfs_linux.go          挂载、pivot_root、设备及路径保护
│   │   ├── process_linux.go         UID/GID、附加组、工作目录与 rlimits
│   │   └── security_linux.go        capabilities 与 no_new_privs
│   └── cgroup/                     cgroup v2 资源管理
│       ├── group_linux.go           创建、加入进程、删除与句柄管理
│       └── resources_linux.go       OCI 资源限制转换与控制文件写入
├── docs/                           按主题编号的中文运行时笔记
├── testdata/bundles/                本地测试 bundle，不纳入 Git
├── AGENTS.md                       工程与文档约定
└── go.mod
```

上图省略测试文件。`_linux.go` 只在 Linux 上参与编译。

`command` 负责用户交互，运行时模块不依赖 CLI 框架。`spec` 把 bundle 转为可用配置；`container` 决定父子进程的调用顺序和失败清理；`linux` 提供底层操作；`cgroup` 独立管理资源组的生命周期。

阅读初始化流程时，先看 `container/process_linux.go` 中的 `startInit`，再看 `container/init_linux.go` 中的 `setupInit`，最后跟进对应的 `linux`、`cgroup` 函数。

当前公开命令是 `validate`。初始化流程通过内部入口和测试验证，完成隔离及权限设置后退出，尚未执行用户程序；`run/create/start`、user namespace 和 seccomp 等能力仍待实现。

## 技术笔记

`docs` 按开发涉及的主题依次编号，简要介绍相关概念和原理，采用 `00-中文主题.md`、`01-中文主题.md` 等名称。

- [00 - OCI 配置读取](docs/00-OCI配置读取.md)
- [01 - Linux 命名空间](docs/01-Linux命名空间.md)
- [02 - rootfs 与挂载](docs/02-rootfs与挂载.md)
- [03 - 容器初始化进程](docs/03-容器初始化进程.md)
- [04 - 容器进程属性](docs/04-容器进程属性.md)
- [05 - cgroup v2 资源管理](docs/05-cgroup-v2资源管理.md)
- [06 - 进程能力与禁止提权](docs/06-进程能力与禁止提权.md)

## 参考资料

- [OCI Runtime Spec v1.3.0](https://github.com/opencontainers/runtime-spec/tree/v1.3.0)
- [OCI Runtime and Lifecycle](https://github.com/opencontainers/runtime-spec/blob/v1.3.0/runtime.md)
- [Linux cgroup v2 文档](https://docs.kernel.org/admin-guide/cgroup-v2.html)
- [PID namespace 语义](https://man7.org/linux/man-pages/man7/pid_namespaces.7.html)
- [setns 限制](https://man7.org/linux/man-pages/man2/setns.2.html)
- [containerd Runtime v2](https://github.com/containerd/containerd/blob/main/docs/runtime-v2.md)
