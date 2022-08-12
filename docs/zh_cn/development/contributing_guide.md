---
sidebar_label: 贡献指南
sidebar_position: 1
---

# JuiceFS 贡献指南

## 基本准则

- 在开始修复功能或错误之前，请先通过 GitHub、Slack 等渠道与我们沟通。此步骤的目的是确保没有其他人已经在处理它，如有必要，我们将要求您创建一个 GitHub issue。
- 我们使用 GitHub issue 来讨论功能并达成一致。这是为了防止彼此的时间被浪费。
- 如果这是一个重大的特性更新，我们强烈建议你写一份设计文档来帮助社区理解你的动机和解决方案。
- 对于首次贡献者来说找到合适 issue 的好方法是使用标签 ["kind/good-first-issue"](https://github.com/juicedata/juicefs/labels/kind%2Fgood-first-issue) 或 ["kind/help-wanted"](https://github.com/juicedata/juicefs/labels/kind%2Fhelp-wanted) 搜索未解决的问题。

## 代码风格

- 我们遵循 ["Effective Go"](https://go.dev/doc/effective_go) 和 ["Go Code Review Comments"](https://github.com/golang/go/wiki/CodeReviewComments)。
- 在提交前使用 `go fmt` 格式化你的代码。你可以在 [Go 的编辑器和 IDE](https://github.com/golang/go/wiki/IDEsAndTextEditorPlugins) 中找到支持 Go 的相关工具的信息。
- 如果你看到任何明显违反风格指南的代码，请修复它并提交一个 PR。
- 每个新的源文件都必须以许可证头开始。
- 安装 [pre-commit](https://pre-commit.com) 并使用它来设置一个预提交钩子来进行静态分析。只需在仓库根目录下运行 `pre-commit install` 即可。

## 签署 CLA

在您为 JuiceFS 进行贡献之前，您需要签署[贡献者许可协议](https://cla-assistant.io/juicedata/juicefs)。当你第一次提交 PR 的时候，将有一个 CLA 助手指导你。

## 什么是好的 PR

- 足够的单元测试
- 遵循编码风格
- 足够的行内注释
- 简要解释的提交内容

## 贡献流程

这是一个贡献者工作流程的大致轮廓：

1. 基于主分支创建一个要贡献的主题分支。这个主分支通常是 `main` 分支；
1. 提交代码；
1. 确保提交消息的格式正确；
1. 将主题分支中的更改推到个人 fork 的仓库；
1. 提交一个 PR 到 [juicedata/juicefs](https://github.com/juicedata/juicefs/compare) 仓库。这个 PR 应该链接到你或其他人创建的一个 issue；
1. PR 在合并之前必须得到至少一个维护者的批准。

尽情享受极客的快乐吧！
