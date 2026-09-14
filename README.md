# minirunc

minirunc 是一个使用 Go 实现的精简 Linux 容器运行时，通过写这个项目加深对 Linux、容器的认识。职责对齐 runc，面向 OCI Runtime Spec 设计。

项目围绕同一条容器生命周期逐步实现 namespace、rootfs 与挂载管理、cgroup v2、信号与进程回收，以及 capabilities、`no_new_privs`、seccomp 等安全机制。

## 技术笔记

`docs` 按开发涉及的主题依次编号，简要介绍相关概念和原理，采用 `00-中文主题.md`、`01-中文主题.md` 等名称。

- [00 - OCI 配置读取](docs/00-OCI配置读取.md)

## 参考资料

- [OCI Runtime Spec v1.3.0](https://github.com/opencontainers/runtime-spec/tree/v1.3.0)
- [OCI Runtime and Lifecycle](https://github.com/opencontainers/runtime-spec/blob/v1.3.0/runtime.md)
- [Linux cgroup v2 文档](https://docs.kernel.org/admin-guide/cgroup-v2.html)
- [PID namespace 语义](https://man7.org/linux/man-pages/man7/pid_namespaces.7.html)
- [setns 限制](https://man7.org/linux/man-pages/man2/setns.2.html)
- [containerd Runtime v2](https://github.com/containerd/containerd/blob/main/docs/runtime-v2.md)
