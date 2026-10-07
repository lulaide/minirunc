# cgroup v2 资源管理

namespace 隔离进程看到的系统视图，cgroup 管理一组进程的资源。cgroup namespace 只隔离 cgroup 路径视图，不会自动创建资源限制。

## 目录与控制文件

cgroup v2 使用一棵统一目录树，通常挂载在 `/sys/fs/cgroup`。创建子目录就创建了一个 cgroup，内核提供其中的控制文件；写入文件是在向内核设置资源策略，不是在保存普通配置文件。

父级的 `cgroup.controllers` 表示可用控制器，`cgroup.subtree_control` 决定哪些控制器提供给子级。可用不等于已启用。运行时应管理明确授权的子树，不能随意修改宿主机上其他服务的 cgroup。

域控制器通常要求父组没有直属进程，才能在 `cgroup.subtree_control` 中启用。运行时不会迁移宿主服务去满足这个条件；应由调用方提供准备好的父树。启用成功后，删除一个叶子组不关闭父级控制器，以免影响其他子组。

当前父树通过宿主运行时参数指定，不随配置管道传给容器。`linux.cgroupsPath` 仅支持父树下的一个目录名，不支持绝对路径、多级路径、systemd scope 格式或接管已有组。不存在的组可以创建，已有目录必须报错。

## OCI 配置与限制

| OCI 字段 | cgroup v2 文件 | 含义 |
| --- | --- | --- |
| `linux.resources.memory.limit` | `memory.max` | 内存上限，单位字节 |
| `linux.resources.cpu.quota/period` | `cpu.max` | 每个周期允许消耗的 CPU 时间，单位微秒 |
| `linux.resources.pids.limit` | `pids.max` | 任务数量上限，线程也计入 |

`cpu.max` 有两个值。例如 `50000 100000` 表示每 100 毫秒允许消耗 50 毫秒 CPU 时间，是时间预算，不是绑定某个 CPU。新建 cgroup 的默认值为 `max 100000`。

OCI 中 `-1` 转换为控制文件中的 `max`，表示本级不设置上限；父级限制仍然生效。内存和 PID 的 `0` 是实际零上限，不能当作未配置。未配置的字段不写入，保留内核继承或默认行为。

当前仅支持表中的字段。其他资源配置需要对应实现，不能静默忽略；特别是设备访问控制在 cgroup v2 中不能照搬 v1 的设备控制文件。

## 启动与清理

应先创建 cgroup、设置限制，再把宿主视角的初始化进程 PID 写入 `cgroup.procs`，确认成功后才让子进程继续初始化。后续创建的子进程继承所属 cgroup。

写入 `cgroup.procs` 会迁移整个进程的线程组。`pids.max` 限制的是进程和线程总数，不是 PID 数值；迁移任务本身可能让数量超过上限，上限主要阻止后续创建任务。Go 初始化程序在加入前已经启动了 runtime，因此现有“启动后迁移”流程不能声称从进程诞生起就完成了全部资源记账。

如果配置了 cgroup namespace，则在加入目标组之后再创建，否则它的路径视图根可能仍是宿主父组。Go 初始化线程保持锁定，在该线程创建 namespace、挂载和最终执行用户程序；锁定不意味着其他 Go 线程消失。

多个控制文件的写入不是事务，失败时不能假设全部没有生效。因此初始设置针对尚未加入进程的新 cgroup，失败时由调用方清理。删除 cgroup 使用移除目录操作，而不是递归删除内核控制文件；删除前必须确保其中没有进程和子 cgroup。

父进程遇到失败或取消时先终止子进程并 `Wait`，然后删除自己创建的 cgroup、关闭目录句柄。清理失败需要向上层返回错误；关闭句柄不等于删除 cgroup。当前初始化入口成功后也会退出，因此成功路径同样清理；后续运行用户程序时，清理时机应延长到容器进程退出之后。

参考：[Linux cgroup v2 文档](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html)、[OCI Linux 资源配置](https://github.com/opencontainers/runtime-spec/blob/v1.3.0/config-linux.md)。
