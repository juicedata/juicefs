---
sidebar_position: 1
slug: /production_deployment_recommendations
---

# Production Deployment Recommendations

This article aims to give some recommendations when deploying JuiceFS to a production environment, so please read the following in advance and carefully.

## Metrics Collection and Visualization

Be sure to collect and visualize the monitoring metrics for your JuiceFS client through Grafana, as described in this [documentation](... /administration/monitoring.md).

## Automatic Metadata Backup

:::tip
Automatic metadata backup is a feature that has been added since JuiceFS v1.0.0
:::

Metadata is critical to JuiceFS file system, and any loss or corruption of metadata may affect a large number of files or even the entire file system. Therefore, metadata must be backed up regularly.

This feature is enabled by default and the backup interval is 1 hour. The backed up metadata will be compressed and stored in the corresponding object storage (isolated from the file system data). Backups are performed by JuiceFS clients, which may cause an increase in CPU and memory usage during the process, and by default it is assumed that one client will be randomly selected to perform the operation.

Note in particular that this feature will be turned off when the number of files reaches **one million**. To turn it on again, you have to set a larger backup interval (the `--backup-meta` option). The interval is configured independently for each client, and the automatic backup can be disabled with `--backup-meta 0`.

:::note
The time required to back up metadata depends on the specific metadata engine, and different metadata engines will have different performance.
:::

For a detailed description of automatic metadata backups, please refer to this [documentation](. /administration/metadata_dump_load.md#Automatic Backup), or you can back up metadata manually. In addition, please also follow the O&M recommendations of the metadata engine you are using to back up your data regularly.

## Trash

:::tip
The Trash feature has been added since JuiceFS v1.0.0
:::

Trash is enabled by default, and the retention time for deleted files defaults to 1 day, which can effectively prevent the risk of loss when data is deleted by mistake.

However, when the trash is enabled, it may also bring some side effects. If the application needs to frequently delete files or overwrite them, it will cause the object storage usage to be much larger than the file system. This is because the JuiceFS client will keep deleted files and overwritten blocks on the object storage for a period of time. Therefore, when deploying JuiceFS to a production environment, it is highly recommended to evaluate the workload ahead to get a proper trash configuration. The retention time can be configured in the following way (`--trash-days 0` means to disable trash)

- New filesystem: set via the `--trash-days <value>` option of `juicefs format`
- Existing filesystem: modify with `--trash-days <value>` option of `juicefs config`

Please refer to this [documentation](../security/trash.md) for more information about the trash.

## Client Background Tasks

All clients of the same JuiceFS volume share a set of background tasks during runtime. Each task is executed at regular intervals, and which client will be chosen is random. The background tasks include

1. cleaning up files and objects to be deleted
2. clearing out-of-date files and fragments in the trash
3. cleaning up stale client sessions
4. automatic backup of metadata

Since these tasks take up some resources when executed, you can set the `--no-bgjob` option to disable them for clients with heavy workload.

:::note
Make sure that at least one JuiceFS client can execute background tasks
:::

## Client Log Rotation

When running a JuiceFS mount point in the background, the client will output the log to a local file by default. The path to the local log file is slightly different depending on the user running the process: for the root user, the path is `/var/log/juicefs.log`, and for others, it is `$HOME/.juicefs/juicefs.log`.

The local log file is not rotated by default and needs to be configured manually in production to ensure that it does not take up too much disk space. The following is a configuration example for log rotation

```text title="/etc/logrotate.d/juicefs"
/var/log/juicefs.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    copytruncate
}
```

You can check the correctness of the configuration file with the `logrotate -d` command.

```shell
logrotate -d /etc/logrotate.d/juicefs
```

Please refer to this [link](https://linux.die.net/man/8/logrotate) for details about the logrotate configuration.

## Command Line Auto-completion

JuiceFS provides command line auto-completion scripts for Bash and Zsh to facilitate the use of `juicefs` commands. Please refer to this [documentation](../reference/command_reference.md#Auto-completion) for details.