---
title: 贡献指南
sidebar_position: 1
description: JuiceFS 是开源软件，代码由全球开发者共同贡献和维护，您可以参考本文了解参与开发的流程和注意事项。
---

## 学习源码 {#learn-source-code}

假定你已经熟悉 Go 语言，充分了解 [JuiceFS 技术架构](https://juicefs.com/docs/zh/community/architecture)，[JuiceFS 源码](https://github.com/juicedata/juicefs)的大体结构如下：

* [`cmd`](https://github.com/juicedata/juicefs/tree/main/cmd) 是代码结构总入口，所有相关功能都能在此找到入口，如 `juicefs format` 命令对应着 `cmd/format.go`；
* [`pkg`](https://github.com/juicedata/juicefs/tree/main/pkg) 是具体实现，核心逻辑都在其中：
  * `pkg/fuse/fuse.go` 是 FUSE 实现的入口，提供抽象 FUSE 接口；
  * `pkg/vfs` 是具体的 FUSE 接口实现，元数据请求会调用 `pkg/meta` 中的实现，读请求会调用 `pkg/vfs/reader.go`，写请求会调用 `pkg/vfs/writer.go`；
  * `pkg/meta` 目录中是所有元数据引擎的实现，其中：
    * `pkg/meta/interface.go` 是所有类型元数据引擎的接口定义
    * `pkg/meta/redis.go` 是 Redis 数据库的接口实现
    * `pkg/meta/sql.go` 是关系型数据库的接口定义及通用接口实现，特定数据库的实现在单独文件中（如 MySQL 的实现在 `pkg/meta/sql_mysql.go`）
    * `pkg/meta/tkv.go` 是 KV 类数据库的接口定义及通用接口实现，特定数据库的实现在单独文件中（如 TiKV 的实现在 `pkg/meta/tkv_tikv.go`）
  * `pkg/object` 是与各种对象存储对接的实现。
* [`sdk/java`](https://github.com/juicedata/juicefs/tree/main/sdk/java) 是 Hadoop Java SDK 的实现，底层依赖 `sdk/java/libjfs` 这个库（通过 JNI 调用）。

JuiceFS 的读写请求处理流程可以阅读[这里](../introduction/io_processing.md)，关键数据结构可以阅读[「内部实现」](./data_structures.md)。

另外，也可以阅读网易存储团队的工程师写的这几篇博客（注意文章内容可能与最新版本代码有出入，一切请以代码为准）：[JuiceFS 调研（基于开源版本代码）](https://aspirer.wang/?p=1560)、[JuiceFS 源码阅读 - 上](https://mp.weixin.qq.com/s/mdqFJLpaJ249rUUEnRiP3Q)、[JuiceFS 源码阅读 - 中](https://mp.weixin.qq.com/s/CLQbQ-cLLGFsShPKUrCUJg)。

## 基本准则 {#guidelines}

- 在开始修复功能或错误之前，请先通过 GitHub、Slack 等渠道与我们沟通。此步骤的目的是确保没有其他人已经在处理它，如有必要，我们将要求您创建一个 GitHub issue。
- 在开始贡献前，使用 GitHub issue 来讨论功能实现并与核心开发者达成一致。
- 如果这是一个重大的特性更新，写一份设计文档来帮助社区理解你的动机和解决方案。
- 对于首次贡献者来说，找到合适 issue 的好方法是使用标签 ["kind/good-first-issue"](https://github.com/juicedata/juicefs/labels/kind%2Fgood-first-issue) 或 ["kind/help-wanted"](https://github.com/juicedata/juicefs/labels/kind%2Fhelp-wanted) 搜索未解决的问题。

## 代码风格 {#coding-style}

- 我们遵循 ["Effective Go"](https://go.dev/doc/effective_go) 和 ["Go Code Review Comments"](https://github.com/golang/go/wiki/CodeReviewComments)。
- 在提交前使用 `go fmt` 格式化你的代码。你可以在 [Go 的编辑器和 IDE](https://github.com/golang/go/wiki/IDEsAndTextEditorPlugins) 中找到支持 Go 的相关工具的信息。
- 每个新的源文件都必须以许可证头开始。
- 安装 [pre-commit](https://pre-commit.com) 并使用它来设置一个预提交钩子来进行静态分析。只需在仓库根目录下运行 `pre-commit install` 即可。

## 签署 CLA {#sign-the-cla}

在您为 JuiceFS 进行贡献之前，您需要签署[贡献者许可协议](https://cla-assistant.io/juicedata/juicefs)。当你第一次提交 PR 的时候，将有一个 CLA 助手指导你。

## 什么是好的 PR {#what-is-a-good-pr}

- 足够的单元测试
- 遵循编码风格
- 足够的行内注释
- 简要解释的提交内容

## 贡献流程 {#contribution-flow}

1. 基于主分支创建一个要贡献的主题分支。这个主分支通常是 `main` 分支；
1. 提交代码；
1. 确保提交消息的格式正确；
1. 将主题分支中的更改推到个人 fork 的仓库；
1. 提交一个 PR 到 [`juicedata/juicefs`](https://github.com/juicedata/juicefs/compare) 仓库。这个 PR 应该链接到你或其他人创建的一个 issue；
1. PR 在合并之前必须得到至少一个维护者的批准。
