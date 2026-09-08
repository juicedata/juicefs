---
title: 元数据 Changelog
sidebar_position: 5
description: 了解 JuiceFS 元数据 changelog 的适用场景、启用方式、保留策略、读取方式，以及如何基于 changelog 构建增量同步。
---

## 功能概览 {#overview}

元数据 changelog 用于记录 JuiceFS 文件系统中的元数据操作，例如创建文件、删除文件、重命名目录项等。它适合用于操作审计、问题排查、构建需要跟随元数据变更的外部消费程序，也可以作为将一个文件系统增量同步到另一个文件系统的变更源。

这是一个 beta 功能，要求 JuiceFS v1.4.0 及以上版本。

:::note 说明
changelog 条目保存在元数据引擎中。该功能默认关闭。开启后会给元数据引擎带来额外写入和存储开销。

changelog 记录的是元数据操作，不包含文件数据内容。但它仍可能包含敏感元数据，例如文件名、扩展属性值以及委托令牌相关操作，请将 changelog 输出视为敏感数据处理。
:::

## 启用与保留策略 {#enable-and-retention}

使用 `juicefs config` 启用或关闭 changelog：

```shell
juicefs config META-URL --changelog
juicefs config META-URL --changelog=false
```

启用 changelog 时，如果没有显式设置 `--changelog-max-age`，JuiceFS 会将默认保留时间设置为 2 小时。可以通过 `--changelog-max-age` 和 `--changelog-max-lines` 控制保留窗口：

```shell
juicefs config META-URL --changelog-max-age 2h --changelog-max-lines 1000000
```

将 `--changelog-max-age` 或 `--changelog-max-lines` 设为 `0` 可以关闭对应的清理规则。changelog 条目由客户端后台任务清理；对于元数据写入频繁的工作负载，应谨慎设置保留策略，避免元数据持续膨胀。

## 读取 changelog {#tail-changelog}

使用 `juicefs changelog` 跟踪 changelog 条目：

```shell
juicefs changelog META-URL
```

未设置 `--from` 或者 `--from` 为 `0` 时，命令会从当前最新的 changelog 版本开始等待新条目，并持续运行，直到被中断。

如果需要从已知版本继续消费，可以将上次处理完成的版本传给 `--from`：

```shell
juicefs changelog META-URL --from 100
```

外部消费程序应保存已经处理完成的最新版本，并在重启后传给 `--from`。在 TKV 场景下，命令可能因为 rewind 窗口输出已经处理过的条目，消费程序需要按 changelog 版本或业务侧幂等逻辑去重。

## 用于增量同步 {#incremental-sync}

changelog 可以作为自定义增量同步程序的变更源。

### 推荐流程 {#recommended-workflow}

1. 在源文件系统上启用 changelog，并设置足够的保留窗口，确保初始备份、加载和消费程序中断期间的 changelog 不会被清理。
2. 从源文件系统创建元数据备份，并将该备份完整 load 到另一个文件系统。备份中会记录创建备份时最新的 changelog 版本。
3. 建议使用二进制格式元数据备份作为初始基线，因为一致性更好，也更适合作为后续基于 changelog 做增量同步的起点。
4. 将备份中记录的版本作为起点，通过 `juicefs changelog SOURCE-META-URL --from VERSION` 启动消费程序。
5. 将输出的每个元数据操作转换并应用到目标文件系统，同时持久化已经处理完成的最新 changelog 版本，以便消费程序重启后继续。

### TKV 的 rewind 窗口 {#tkv-rewind-window}

如果源端元数据引擎是 TKV，需要额外注意 changelog 版本使用的是事务 `startTs`，而不是事务提交时间。事务可能在元数据备份记录最新 changelog 版本之前开始，但在备份创建完成之后才提交。如果只从备份记录版本之后读取，就可能漏掉这类条目。

为避免遗漏，消费程序需要从备份记录版本之前回退一个窗口开始读取，并对已经应用过的条目去重。`juicefs changelog` 在 TKV 场景下会内置执行这个 rewind；TiKV 默认 rewind 窗口是 10 秒 TSO 时间，也可以通过 `JFS_TKV_REWIND` 环境变量调整。

TKV 元数据备份会包含这个 rewind 窗口内的 changelog 条目，因此消费程序可以用备份里的这些条目建立初始去重集合。后续从 `juicefs changelog` 读取到相同版本的条目时，应跳过已经在基线备份中应用过的内容。

## 输出格式 {#output-format}

每行输出格式如下：

```text
VERSION: UNIX_SECONDS.NANOSECONDS|OPERATION(arguments)[:result]|(SESSION_ID,TXN_ID)
```

- `VERSION`：changelog 游标。
- `UNIX_SECONDS.NANOSECONDS`：操作发生的时间戳。
- `OPERATION(arguments)[:result]`：元数据操作及其内部参数。部分操作会在 `:` 后追加结果，例如新创建的 inode。
- `SESSION_ID`：产生该条目的 JuiceFS 客户端会话 ID。
- `TXN_ID`：该客户端会话内的事务 ID。

示例：

```text
101: 1716440752.123456789|CREATE(1,report.txt,1000,1000,1,420,18,,Keep,true):1024|(3,88)
102: 1716440753.000000000|WRITE(1024,0,0,233344,4096,1716440753,0):1|(3,89)
103: 1716440760.000000000|UNLINK(1,report.txt,0,false,true):1024|(3,90)
```

## 使用建议和限制 {#notes}

- changelog 不是元数据备份。备份和恢复应使用[元数据备份](metadata_dump_load.md)。
- changelog 不包含文件数据内容，不能单独用来还原文件。
- 如果旧条目已经被清理，`juicefs changelog --from` 无法恢复中间缺失的条目。
- 启用 changelog 会增加元数据引擎写入。对于元数据密集型工作负载，使用较长保留时间前应先评估开销。
