---
title: Usage Tracking
sidebar_position: 4
---

JuiceFS by default collects and reports **anonymous** usage data. It only collects core metrics (e.g. version number, file system size), no user or any sensitive data will be collected. You could review related code [here](https://github.com/juicedata/juicefs/blob/main/pkg/usage/usage.go).

These data help us understand how the community is using this project. You could disable reporting easily by command line option `--no-usage-report`:

```
juicefs mount --no-usage-report
```
