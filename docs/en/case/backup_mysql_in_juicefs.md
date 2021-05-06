# 基于 JuiceFS 的 MySQL 备份、验证和恢复方案

MySQL 通常包含了公司最重要的产品和用户信息，是公司的核心资产，一旦丢失或者损坏会导致非常严重的损失（甚至破产），一定要做多重备份以确保数据安全。我们将介绍如何使用 JuiceFS 来大大简化 MySQL 的备份、验证和恢复过程，有效保障数据安全。

JuiceFS 非常适合存储 MySQL 的备份数据，因为：

1. 它是 POSIX 兼容的文件系统，可以直接将备份数据写入 JuiceFS，也可以在 JuiceFS 上运行 MySQL，而不需要经过本地磁盘中转（包括验证和恢复数据），这将节省大量时间，通常是数小时到数天，这些时间在数据恢复时是非常宝贵的。
2. 它支持透明的数据压缩和存储加密，避免了手动压缩和加密的麻烦，有效地降低存储成本和保障数据隐私，同样也能节省大量的备份和恢复时间。
3. 它支持以目录为单位的快照功能，可以很方便地做备份验证和恢复。
4. 它支持所有公有云的所有区，可以很方便地做异地备份。
5. 它的容量是弹性伸缩的，你只需要按实际使用量付费，完全不用做容量规划和扩容。

## 逻辑备份

逻辑备份是将 MySQL 里面的数据以简单的文本格式（最不容易损坏的格式）存储，常用工具包括 mysqldump 和 mydumper。mysqldump 是 MySQL 原生的备份工具，稳定好用，但是比较慢。mydumper 支持多线程导出，速度更快，推荐使用 mydumper。

目前没有增量逻辑备份的工具，理论上可以对比两次全量备份的数据差异，使用 diff 生成一个差异文件作为增量备份保存。

逻辑备份比较慢，如果数据库太大，推荐每个周末或者每个月做一次逻辑备份。如果数据库比较小，也可以每天或者每小时做逻辑备份。对于公有云中已经有备份功能的 RDS，不同产品备份策略不尽相同，也建议你再做一次逻辑备份，以规避 RDS 的备份故障导致的风险。

### 备份

逻辑备份的数据可以直接写到 JuiceFS 挂载目录（假定是 /jfs ）中，比如用 mydumper 导出数据：

```
mydumper -B <db_name> --outputdir /jfs/<db_name>_data
```

逻辑备份的文本数据非常容易被压缩，JuiceFS 会默认使用当前最先进的 ZStandard 压缩算法将数据压缩后再保存，有非常好的写入性能（高达 300MB/s），比 mydumper 的压缩功能快很多。

### 验证

定时备份并不意味着高枕无忧，因为备份也可能出问题，验证备份是十分必要的。对于 MySQL 备份而言，备份是否真的可用，只有用备份进行一次恢复才知道。

验证逻辑备份，可以启用一个空的 MySQL 实例进行恢复，并对恢复后的 MySQL做一些完整性检查，比如检查数据量大小等。

### 恢复

从逻辑备份恢复数据时，先启动一个空的 MySQL 实例，再用 myloader 导入数据：

```
myloader -B <db_name> --directory /jfs/<db_name>_data
```

## 物理备份

物理备份是指备份 MySQL 实际使用的数据文件，业界通常使用 Percona 公司提供的开源工具 XtraBackup 直接读 MySQL 的数据文件进行备份，而不需要经过 MySQL 进行逻辑解析，会快很多。

XtraBackup 支持全量备份和增量备份，建议在一定周期内两者交替进行。比如每周末做一次全量备份，之后的 6 天内每天做一次增量备份，备份数据保存 4 周以上。

### 备份

用 xtrabackup 做物理备份，推荐对整个实例的所有数据库做备份，而不是指定某些数据库。

给整个 MySQL 实例的数据做一个全量备份

```
xtrabackup --backup --target-dir=/jfs/base/
```

之后可以做增量备份以缩减数据量和备份时间，比如：

```
xtrabackup --backup --incremental-basedir=/jfs/base --target-dir=/jfs/incr1
```

再之后的增量备份，可以继续以全量备份为基准，也可以以上一次增量备份为基准，比如：

```
xtrabackup --backup --incremental-basedir=/jfs/base --target-dir=/jfs/incr2
```

**推荐统一使用全量备份做基准来做增量备份，虽然数据量会大一点，但恢复流程会更简单，不容易出错。**

### 验证

验证增量备份时，需要把增量数据附加到全量备份上，此时可以利用 JuiceFS 的快照功能快速拷贝一个全量备份用于验证，验证完成后再删掉 。

基于刚才创建的全量备份建一个 snapshot，因为在 MySQL 运行的时候会修改数据文件，如果直接运行在备份数据上，就把备份破坏了。

```
juicefs snapshot /jfs/base /jfs/base_snapshot
```

因为 xtrabackup 做 apply log 时有大量随机写，建议 JuiceFS 挂载时加上 –writeback 参数来优化随机写的性能。

准备数据

```
xtrabackup --prepare --apply-log-only --target-dir=/jfs/base_snapshot
```

如果是验证增量备份，需要在全量备份数据上做叠加处理（下一节恢复中有讲解），比如：

```
xtrabackup --prepare --apply-log-only --incremental-dir=/jfs/incr1 --target-dir=/jfs/base_snapshot
```

启动一个新的 MySQL 实例，把数据目录指向 /jfs/base_snapshot。如果正常启动，说明备份正确。生产环境中，还可以把这个 MySQL 实例作为 slave 和 master 实例做同步来确认备份正确。（[我们的客户案列中有详细代码与说明](https://juicefs.com/blog/cn/posts/xiachufang-mysql-backup-practice-on-juicefs/)）

### 恢复

从物理备份进行数据库重建时，需要先找到某个全量备份，拷贝到即将要运行的 MySQL 数据目录中：

```
rsync -avP /jfs/base/* /var/lib/mysql
```

1. 如果只需要用全量备份进行恢复，做如下的准备：

```
xtracbackup --prepare --target-dir=/var/lib/mysql
```

1. 如果是需要从增量备份恢复，则需要加上 –apply-log-only 参数:

```
xtracbackup --prepare --apply-log-only --target-dir=/var/lib/mysql
```

然后再叠加增量备份：

```
xtrabackup --prepare --incremental-dir=/jfs/incr1 --target-dir=/var/lib/mysql
```

注意：如果还有其他的增量备份，也需要加上 –apply-log-only 参数。

之后需要修改文件属性让 MySQL 实例可读写，再在启动 MySQL 服务，完成恢复操作。

以上是一个简单的全量与增量备份和数据恢复的流程，更多的细节请参考 xtrabackup 的官方文档。

## 事务日志备份

逻辑备份和物理备份都只能以一定周期执行，在两次备份任务之间的数据变化，可以用事物日志 binlog 记录和恢复。所以备份 binlog 也是非常重要的事情。

首先要在 MySQL 配置文件中开启 log-bin 选项并重启 MySQL 让修改生效。然后做一个定时任务，同步 binlog 到 JuiceFS 的挂载目录中。

```
rsync -au --append /var/log/mysql/mysql-bin.* /jfs/backup/binlogs/
```

注意，在 MySQL 生产配置中 log-bin 和 datadir 应该指向不同的盘，以避免一块盘坏了，两部分数据一起丢失的惨剧。

使用 binlog 日志进行恢复时，只需将它们拷贝到 MySQL 的日志目录，再按照 MySQL 的指令让它重播 binlog 到指定位置。

## 快速恢复

当我们做好了物理和逻辑备份，也做好 binlog 备份之后，如果发生意外，需要恢复数据，很重要的事情是确认要恢复到的状态点（Pos）或时间点。

根据要恢复到的状态（时间点）选择最近的全量备份和增量备份，再在此基础上播放之间的 binlog。

根据恢复后的用途，可以选择是在 JuiceFS 上进行恢复，还是拷贝到适当的本地存储后再做恢复：

1. 如果是在恢复后做一些简单的查询，而且需要重播的事务日志不太多，可以直接在 JuiceFS 上做个快照后进行恢复，和前面物理备份一节中做备份验证的方法一样。
2. 如果是要恢复生产环境的实例，需要把数据从 JuiceFS 从拷贝到本地盘等，再做恢复。

## 性能优势

为了节省存储空间和保护数据安全，通常会对备份数据压缩并加密后再存储，验证和恢复又需要解密和解压缩，整个过程非常费时和费空间。因为 JuiceFS 内部实现了透明的数据压缩和加密，我们可以大大简化备份、验证和恢复过程，同时大大缩短备份和恢复的用时。以 1.5T 的 MySQL 数据的数据库为例，使用 JuiceFS 的备份方案可以将原来的 18 小时缩短为 2 小时左右，如下图所示：

![_images/mysql-backup-comparison.png](https://juicefs.com/docs/zh/_images/mysql-backup-comparison.png)

在做数据恢复时，时间更为宝贵，如果是做局部数据恢复，可以在几分钟内在 JuiceFS 上直接启动 MySQL 进行数据恢复。即使重建完整数据库，也会比其他方式快很多，如下图所示：

![_images/mysql-restore-comparison.png](https://juicefs.com/docs/zh/_images/mysql-restore-comparison.png)

