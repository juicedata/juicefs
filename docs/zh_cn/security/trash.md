---
sidebar_position: 2
---

# 回收站

:::note 注意
此特性需要使用 1.0.0 及以上版本的 JuiceFS。旧版本 JuiceFS 欲使用回收站，需要在升级所有挂载点后通过 `config` 命令手动设置回收站，详见下方示范。
:::

JuiceFS 默认开启回收站功能，你删除的文件会被保存在文件系统根目录下的 `.trash` 目录内，保留指定时间后才将数据真正清理。在清理到来之前，通过 `df -h` 命令看到的文件系统使用量并不会减少，对象存储中的对象也会依然存在。

不论你正在用 `format` 命令初始化文件系统，还是用 `config` 命令调整已有的文件系统，都可以用 [`--trash-days`](../reference/command_reference.md#format) 参数来指定回收站保留时长：

```shell
# 初始化新的文件系统
juicefs format META-URL myjfs --trash-days=7

# 修改已有文件系统
juicefs config META-URL --trash-days=7

# 设置为 0 以禁用回收站
juicefs config META-URL --trash-days=0
```

### 恢复文件 {#recover}

文件被删除时，会根据删除时间，被保存在格式为 `.trash/YYYY-MM-DD-HH/[parent inode]-[file inode]-[file name]` 的目录，相当于根据文件的被删时间来组织所有回收站文件。只需要确定文件的删除时间，就能在对应的目录中找到他们，来进行恢复操作。注意回收站中目录名称里的时间是精确到小时的，都是从 UTC 时间计算而来，因此要注意时区问题。

如果已经顺利找到想要恢复的文件，只需将其 `mv` 出来即可：

```shells
mv .trash/2022-11-30-10/[parent inode]-[file inode]-[file name] .
```

考虑到回收站内的文件，已经丢失了原目录结构信息，仅在文件名保留 inode 信息，如果你确实忘记了被误删的文件名，可以使用 [`juicefs info`](../reference/command_reference.md#info) 命令先找出父目录的 inode，然后顺藤摸瓜地定位到误删文件。

假设挂载点为 `/jfs`，你误删了 `/jfs/data/config.json`，但无法直接通过 `config.json` 文件名来操作恢复文件（因为你忘了），可以用下方流程反查父目录 inode，然后在回收站中定位文件：

```shell
# 用 info 命令确定父目录 inode
juicefs info /jfs/data

# 在上方的输出中，关注 inode 字段，假设 /jfs/data 这个目录的 inode 为 3
# 使用 find 命令，就能找出该目录下所有被删除的文件
find /jfs/.trash -name '3-*'

# 将该目录下所有文件进行恢复
mv /jfs/.trash/2022-11-30-10/3-* /jfs/data
```

需要注意，只有 root 用户具有回收站目录的写权限，因此只能使用 root 用户能用 `mv` 进行上述恢复操作。普通用户如果有这些文件的读权限，也可以用 `cp` 的方式读取文件，再写到新文件，虽然产生了存储空间浪费，但也能实现恢复文件的效果。

JuiceFS v1.1 提供了 [`restore`](../reference/command_reference.md#restore) 子命令来快速恢复大量误删的文件（TODO：按照文件名 pattern 恢复？）：

```shell
# 运行命令预览恢复文件计划，但并不实际执行恢复
juicefs restore $META_URL 2023-05-10-01

# 确认执行计划无误，增加 --put-back 参数确认执行
juicefs restore $META_URL 2023-05-10-01 --put-back
```

### 彻底删除文件 {#purge}

回收站目录各方面表现都和普通目录一致——恢复文件就是 `mv` 出来，如果想彻底删除文件，那么 `rm` 就能做到（需要 root 权限）。但要注意，**就算文件被彻底删除，也未必会立刻从对象存储释放**，这是因为过期文件的清理由 JuiceFS 客户端定期在后台任务（background job，也称 bgjob）执行，默认每小时清理一次。

因此，为了让过期文件能够正常清理，需要至少 1 个在线的挂载点，并且能够正常执行后台任务（不能开启 [`--no-bgjob`](../reference/command_reference.md#mount)）。后台任务的执行也需要时间，因此面对大量文件过期（或者强制删除）时，对象存储的清理速度未必和你期望的一样快（TODO：如何加速删除？）

在回收站里，除了因用户操作而产生的文件，还存在这另一类对用户不可见的数据——覆写产生的文件碎片。关于文件碎片是怎么产生的，可以详细阅读[「JuiceFS 如何存储文件」](../introduction/architecture.md#how-juicefs-store-files)。总而言之，应用需要经常删除文件或者频繁覆盖写文件，会导致对象存储使用量远大于文件系统用量。

由于对用户不可见，这些失效的文件碎片无法轻易删除（TODO：保留有意义吗？如何恢复？）。如果想要主动清理它们，可以用以下操作手动处理：

```shell
# 临时禁用回收站
juicefs config META-URL --trash-days 0

# 运行 gc 命令删除泄露对象
juicefs gc --delete

# 操作完成后，记得重新开启回收站
```

### 访问权限 {#permission}

所有用户均有权限浏览回收站，可以看到所有被删除的文件。然而 `.trash` 目录只有 root 具备写权限，但就算文件被移入回收站，也会保留原先的文件权限，因此在操作回收站内的文件时，注意权限问题并根据情况调整操作用户。

关于回收站的权限问题，还需要注意：

* 当 JuiceFS 客户端由非 root 用户启动时，需要在 mount 时指定 `-o allow_root` 参数，允许 root 用户访问文件系统，否则将无法正常清空回收站。
* `.trash` 目录只能通过文件系统根目录访问，子目录挂载点无法访问。
* 回收站内不允许用户自行创建新的文件，只有 root 才能删除或移动其中的文件。
