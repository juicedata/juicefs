---
sidebar_position: 2
---

# 回收站

:::note 注意
此特性需要使用 1.0.0 及以上版本的 JuiceFS。旧版本 JuiceFS 欲使用回收站，需要在升级所有挂载点后通过 `config` 命令手动设置回收站，详见下方示范。
:::

JuiceFS 默认开启回收站功能，你删除的文件会被保存在文件系统根目录下的 `.trash` 目录内，保留指定时间后才将数据真正清理。在清理到来之前，通过 `df -h` 命令看到的文件系统使用量并不会减少，对象存储中的对象也会依然存在。

不论你正在用 `format` 命令初始化文件系统，还是用 `config` 命令调整已有的文件系统，都可以用 [`--trash-days`](../reference/command_reference.mdx#format) 参数来指定回收站保留时长：

```shell
# 初始化新的文件系统
juicefs format META-URL myjfs --trash-days=7

# 修改已有文件系统
juicefs config META-URL --trash-days=7

# 设置为 0 以禁用回收站
juicefs config META-URL --trash-days=0
```

另外，回收站自动清理依赖 JuiceFS 客户端的后台任务，为了保证后台任务能够正常执行，需要至少 1 个在线的挂载点，并且在挂载文件系统时不可以使用 [`--no-bgjob`](../reference/command_reference.mdx#mount-metadata-options) 参数。

## 恢复文件 {#recover}

文件被删除时，会根据删除时间，被保存在格式为 `.trash/YYYY-MM-DD-HH/[parent inode]-[file inode]-[file name]` 的目录，其中 `YYYY-MM-DD-HH` 就是删除操作的 UTC 时间。因此只需要确定文件的删除时间，就能在对应的目录中找到他们，来进行恢复操作。

如果已经顺利找到想要恢复的文件，只需将其 `mv` 出来即可：

```shells
mv .trash/2022-11-30-10/[parent inode]-[file inode]-[file name] .
```

被删除的文件会完全丢失其目录结构，在回收站中“平铺”存储，但会在文件名保留父目录的 inode，如果你确实忘记了被误删的文件名，可以使用 [`juicefs info`](../reference/command_reference.mdx#info) 命令先找出父目录的 inode，然后顺藤摸瓜地定位到误删文件。

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

如果误删了结构复杂的目录，用 `mv` 命令手动恢复原样会非常艰难，比方说：

```shell
$ tree data
data
├── app1
│   └── config
│       └── config.json
└── app2
    └── config
        └── config.json

# 删除上方的复杂目录
$ juicefs rmr data

# 文件会在回收站内平铺存储，丢失目录结构
$ tree .trash/2023-08-14-05
.trash/2023-08-14-05
├── 1-12-data
├── 12-13-app1
├── 12-15-app2
├── 13-14-config
├── 14-17-config.json
├── 15-16-config
└── 16-18-config.json
```

正因如此，JuiceFS v1.1 提供了 [`restore`](../reference/command_reference.mdx#restore) 子命令来快速恢复大量误删的文件，以上方目录结构为例，恢复操作如下：

```shell
# 先运行 restore 命令，在回收站内重建目录结构
$ juicefs restore $META_URL 2023-08-14-05

# 预览恢复完毕的目录结构，确定需要恢复的范畴
# 既可以直接用下方命令完整恢复整个目录，也可以单独用 mv 命令恢复某一部分
$ tree .trash/2023-08-14-05
.trash/2023-08-14-05
└── 1-12-data
    ├── app1
    │   └── config
    │       └── config.json
    └── app2
        └── config
            └── config.json

# 增加 --put-back 参数将文件恢复至原位
juicefs restore $META_URL 2023-08-14-05 --put-back
```

## 彻底删除文件 {#purge}

当回收站中的文件到了过期时间，会被自动清理。需要注意的是，文件清理由 JuiceFS 客户端的后台任务（background job，也称 bgjob）执行，默认每小时清理一次，因此面对大量文件过期时，对象存储的清理速度未必和你期望的一样快，可能需要一些时间才能看到存储容量变化。

如果你希望在过期时间到来之前彻底删除文件，需要使用 root 用户身份，用 [`juicefs rmr`](../reference/command_reference.mdx#rmr) 或系统自带的 `rm` 命令来删除回收站目录 `.trash` 中的文件，这样就能立刻释放存储空间。

例如，彻底删除回收站中某个目录：

```shell
juicefs rmr .trash/2022-11-30-10/
```

如果希望更快速删除过期文件，可以挂载多个挂载点来突破单个客户端的删除速度上限。

## 回收站和文件碎片 {#gc}

在回收站里，除了因用户操作而产生的文件，还存在另一类对用户不可见的数据——覆写产生的文件碎片。关于文件碎片是怎么产生的，可以详细阅读[「JuiceFS 如何存储文件」](../introduction/architecture.md#how-juicefs-store-files)。总而言之，如果应用经常删除文件或者频繁覆盖写文件，会导致对象存储使用量远大于文件系统用量。

虽然失效的文件碎片不能直接浏览、操作，但你可以通过 [`juicefs status`](../reference/command_reference.mdx#status) 命令来简单观测其规模：

```shell
# 下方 Trash Slices 就是失效的文件碎片统计
$ juicefs status META-URL --more
...
           Trash Files: 0                     0.0/s
           Trash Files: 0.0 b   (0 Bytes)     0.0 b/s
 Pending Deleted Files: 0                     0.0/s
 Pending Deleted Files: 0.0 b   (0 Bytes)     0.0 b/s
          Trash Slices: 27                    26322.2/s
          Trash Slices: 783.0 b (783 Bytes)   753.1 KiB/s
Pending Deleted Slices: 0                     0.0/s
Pending Deleted Slices: 0.0 b   (0 Bytes)     0.0 b/s
...
```

文件碎片也按照回收站设置的时间进行保留，这对数据安全同样具有重要意义：如果你不小心对文件进行了错误修改，或者覆盖写，一样可以通过元数据备份，把数据找回来（当然，前提是误操作之前已经设置好了元数据备份）。如果确实需要对误修改的文件进行恢复，则需要找回旧版元数据，挂载后手动将文件拷贝出来进行恢复，详见[备份与恢复](../administration/metadata_dump_load.md)。

由于对用户不可见，这些失效的文件碎片无法轻易删除。如果规模巨大，确实需要主动清理它们，可以用以下操作手动处理：

```shell
# 临时禁用回收站
juicefs config META-URL --trash-days 0

# 如果有需要，可以手动触发再次运行碎片合并
juicefs gc --compact

# 运行 gc 命令删除泄露对象
juicefs gc --delete

# 操作完成后，记得重新开启回收站
```

## 访问权限 {#permission}

所有用户均有权限浏览回收站，可以看到所有被删除的文件。然而 `.trash` 目录只有 root 具备写权限，但就算文件被移入回收站，也会保留原先的文件权限，因此在操作回收站内的文件时，注意权限问题并根据情况调整操作用户。

关于回收站的权限问题，还需要注意：

* 当 JuiceFS 客户端由非 root 用户启动时，需要在 mount 时指定 `-o allow_root` 参数，允许 root 用户访问文件系统，否则将无法正常清空回收站。
* `.trash` 目录只能通过文件系统根目录访问，子目录挂载点无法访问。
* 回收站内不允许用户自行创建新的文件，只有 root 才能删除或移动其中的文件。
