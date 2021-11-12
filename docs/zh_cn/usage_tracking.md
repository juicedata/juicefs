# 用量统计

JuiceFS 默认会收集 **匿名** 用量数据。它仅收集核心指标（例如版本号），不会收集任何用户信息或任何敏感数据。您可以在[此处](https://github.com/juicedata/juicefs/blob/main/pkg/usage/usage.go)查看相关代码。

这些数据有助于我们了解社区如何使用此项目。您可以通过命令行选项 `--no-usage-report` 禁用该功能：

```
$ ./juicefs mount --no-usage-report
```

