# 02 - rootfs 与挂载

## rootfs

rootfs 是容器进程看到的根目录。它提供程序、动态链接器、共享库和系统目录，但单独拥有一组文件并不会限制进程访问宿主文件系统。runtime 必须在独立的 mount namespace 中建立挂载，并切换进程的根。

切换前通常先将宿主根挂载设为递归 private，阻止随后产生的挂载事件传播到宿主。rootfs 还要 bind mount 到自身，使它成为独立挂载点，满足 `pivot_root` 的要求。

## pivot_root

`pivot_root(new_root, put_old)` 将当前 mount namespace 的根挂载替换为 `new_root`，并暂时把旧根移动到 `put_old`。典型顺序是：

1. 进入 rootfs，并在其中创建临时的旧根目录。
2. 调用 `pivot_root`，再将工作目录切换到 `/`。
3. 使用 lazy unmount 卸载旧根并删除临时目录。
4. 建立容器配置要求的其他挂载。

旧根卸载后，容器路径和符号链接只能在新根中解析。runtime 不应在旧根仍可访问时执行用户提供的程序。

`chroot` 只改变路径解析使用的根目录，不改变挂载拓扑，特权进程还可能逃离。容器 runtime 通常使用 `pivot_root` 建立更完整的文件系统边界。

## OCI mounts

OCI `mounts` 按数组顺序执行，因此父挂载必须出现在子挂载之前。例如先在 `/dev` 挂载 tmpfs，再挂载 `/dev/pts` 和 `/dev/shm`。

mount options 分为两类：`ro`、`nosuid`、`noexec` 等通用选项转换为 `mount(2)` flags；`mode=755`、`size=65536k`、`newinstance` 等文件系统选项作为 data 传给对应文件系统。private、shared、slave 和 unbindable 属于挂载传播设置，需要在完成挂载后单独应用。

Linux 容器通常挂载 proc、tmpfs、devpts、mqueue 和 sysfs。新的 `/dev` tmpfs 还需要提供 `/dev/null`、`/dev/zero`、随机数设备和终端设备，以及指向 `/proc/self/fd` 的标准文件描述符链接。

## 屏蔽和只读路径

`linux.maskedPaths` 防止容器访问敏感内核接口。文件可以 bind mount `/dev/null` 进行遮蔽，目录可以覆盖一个不可访问的只读 tmpfs。

`linux.readonlyPaths` 保留路径内容，但通过 bind mount 和只读 remount 禁止修改。路径不存在时无需创建，因为容器本来就不能访问它。

这些操作依赖 mount namespace。容器初始化失败或进程退出后，namespace 被内核回收，其中的挂载也随之消失，不应残留到宿主挂载表。
