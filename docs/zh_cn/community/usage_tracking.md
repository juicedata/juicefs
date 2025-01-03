---
title: 用量上报
sidebar_position: 4
---

JuiceFS 默认会收集并上报 **「匿名」** 的使用数据。这些数据仅仅包含核心指标（如版本号、文件系统大小），不会包含任何用户信息或者敏感数据。你可以查看[这里](https://github.com/juicedata/juicefs/blob/main/pkg/usage/usage.go)检查相关代码。

这些数据帮助我们理解社区如何使用这个项目。你可以简单地通过 `--no-usage-report` 选项关闭用量上报：

```
juicefs mount --no-usage-report
```
