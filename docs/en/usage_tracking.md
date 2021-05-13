# Usage Tracking

JuiceFS by default collects **anonymous** usage data. It only collects core metrics (e.g. version number), no user or any sensitive data will be collected. You could review related code [here](https://github.com/juicedata/juicefs/blob/main/pkg/usage/usage.go).

These data help us understand how the community is using this project. You could disable reporting easily by command line option `--no-usage-report`:

```
$ ./juicefs mount --no-usage-report
```
