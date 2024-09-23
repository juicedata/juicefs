---
sidebar_position: 1
slug: /production_deployment_recommendations
description: This article is intended as a reference for users who are about to deploy JuiceFS to a production environment and provides a series of environment configuration recommendations.
---

# Production Deployment Recommendations

This document provides deployment recommendations for JuiceFS Community Edition in production environments. It focuses on monitoring metric collection, automatic metadata backup, trash configuration, background tasks of clients, client log rolling, and command-line auto-completion to ensure the stability and reliability of the file system.

## Metrics collection and visualization

It is necessary to collect monitoring metrics from JuiceFS clients and visualize them using Grafana. This allows for real-time monitoring of file system performance and health status. For detailed instructions, see this [document](../administration/monitoring.md).

## Automatic metadata backup

:::tip
Automatic metadata backup is a feature that has been added since JuiceFS v1.0.0.
:::

Metadata is critical to the JuiceFS file system, and any loss or corruption of metadata may affect a large number of files or even the entire file system. Therefore, metadata must be backed up regularly.

This feature is enabled by default and the backup interval is 1 hour. The backed-up metadata is compressed and stored in the corresponding object storage, separate from file system data. Backups are performed by JuiceFS clients, which may increase CPU and memory usage during the process. By default, one client is randomly selected for backup operations.

It is important to note that this feature is disabled when the number of files reaches **one million**. To re-enable it, set a larger backup interval (the `--backup-meta` option). The interval is configured independently for each client. You can use `--backup-meta 0` to disable automatic backup.

:::note
The time required for metadata backup depends on the specific metadata engine. Different metadata engines have different performance.
:::

For detailed information on automatic metadata backups, see this [document](../administration/metadata_dump_load.md#backup-automatically). Alternatively, you can back up metadata manually. In addition, follow the operational and maintenance recommendations of the metadata engine you are using to back up your data regularly.

## Trash

:::tip
The Trash feature has been available since JuiceFS v1.0.0.
:::

Trash is enabled by default. The retention time for deleted files defaults to 1 day to mitigate the risk of accidental data loss.

However, enabling Trash may have side effects. If the application needs to frequently delete files or overwrite them, it will cause the object storage usage to be much larger than the file system. This is because the JuiceFS client retain deleted files and overwritten blocks on the object storage for a certain period. Therefore, it is highly recommended to evaluate workload requirements before deploying JuiceFS in a production environment to configure Trash appropriately. You can configure the retention time as follows (`--trash-days 0` disables Trash):

- For new file systems: set via the `--trash-days <value>` option of `juicefs format`
- For existing file systems: modify with the `--trash-days <value>` option of `juicefs config`

For more information on Trash, see this [document](../security/trash.md).

## Client background tasks

The JuiceFS file system maintains background tasks through clients, which can automatically execute cleaning tasks such as deleting pending files and objects, purging expired files and fragments from Trash, and terminating long-stalled client sessions.

All clients of the same JuiceFS volume share a set of background tasks during runtime. Each task is executed at regular intervals, with the client chosen randomly. Background tasks include:

- Cleaning up files and objects to be deleted
- Clearing out-of-date files and fragments in Trash
- Cleaning up stale client sessions
- Automatic backup of metadata

Since these tasks take up some resources when executed, you can set the `--no-bgjob` option to disable them for clients with heavy workload.

:::note
Make sure that at least one JuiceFS client can execute background tasks.
:::

## Client log rotation

When running a JuiceFS mount point in the background, the client outputs logs to a local file by default. The path to the local log file is slightly different depending on the user running the process:

- For the root user, the path is `/var/log/juicefs.log`.
- For others, the path is `$HOME/.juicefs/juicefs.log`.

The local log file is not rotated by default and needs to be configured manually in production to prevent excessive disk space usage. The following is a configuration example for log rotation:

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

You can check the correctness of the configuration file with the `logrotate -d` command:

```shell
logrotate -d /etc/logrotate.d/juicefs
```

For details about the logrotate configuration, see this [link](https://linux.die.net/man/8/logrotate).

## Command line auto-completion

JuiceFS provides command line auto-completion scripts for Bash and Zsh to facilitate the use of `juicefs` commands. For details, see this [document](../reference/command_reference.mdx#auto-completion) for details.
