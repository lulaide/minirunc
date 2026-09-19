# 01 - Linux namespace

## 隔离对象

Linux namespace 让一组进程看到独立的系统资源视图。OCI Linux 配置使用 `linux.namespaces` 描述容器需要创建或加入的 namespace。

| OCI 类型 | Linux 标志 | 隔离内容 |
| --- | --- | --- |
| `mount` | `CLONE_NEWNS` | 挂载点和挂载传播关系 |
| `pid` | `CLONE_NEWPID` | 进程 ID 空间 |
| `uts` | `CLONE_NEWUTS` | hostname 和 domainname |
| `ipc` | `CLONE_NEWIPC` | System V IPC 和 POSIX 消息队列 |
| `network` | `CLONE_NEWNET` | 网络设备、路由和端口等网络资源 |
| `cgroup` | `CLONE_NEWCGROUP` | 进程看到的 cgroup 根路径 |
| `user` | `CLONE_NEWUSER` | UID、GID 和 capabilities |
| `time` | `CLONE_NEWTIME` | monotonic 和 boottime 时钟偏移 |

namespace 提供资源视图隔离，本身不等于完整的安全边界。文件系统、权限、cgroup 和系统调用过滤仍需分别配置。

## OCI 配置语义

namespace 项只有 `type` 时，runtime 必须创建该类型的新 namespace；同时包含 `path` 时，runtime 必须加入这个路径指向的已有 namespace。某种类型没有出现在数组中时，容器继承 runtime 进程所在的 namespace。同一类型不能重复出现。

创建和加入是两条不同的系统调用路径：新进程可以通过 `clone` 的 `CLONE_NEW*` 标志进入新 namespace；当前线程可以用 `unshare` 创建，或用 `setns` 加入已有 namespace。`setns` 操作线程属性，在 Go 程序中还要考虑线程锁定和操作顺序。

## PID namespace

PID namespace 对创建者的语义比较特殊。设置 `CLONE_NEWPID` 后，调用者仍在原 PID namespace 中，新创建的子进程成为新 namespace 的 PID 1。

PID 1 负责接收孤儿进程并回收僵尸进程，而且内核对发给 PID 1 的部分信号有特殊处理。因此 runtime 需要在宿主侧保存真实 PID、转发信号并等待容器初始化进程，容器内的初始化进程则必须承担 PID 1 的职责。

## mount namespace

创建 mount namespace 后，runtime 还必须处理挂载传播。通常先将根挂载设为递归 private 或 slave，再建立 rootfs 和容器挂载，防止容器内的挂载变化传播到宿主。

namespace 必须在切换 rootfs 和启动用户程序之前准备完成。runtime 通常 re-exec 自身作为容器初始化进程，使新进程从启动开始就在目标 namespace 中，再由它完成挂载、权限设置和 `execve`。
