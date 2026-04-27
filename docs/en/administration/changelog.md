---
title: Metadata Changelog
sidebar_position: 5
description: Learn the use cases, enablement, retention policy, consumption, and incremental sync workflow for the JuiceFS metadata changelog.
---

## Overview {#overview}

The metadata changelog records metadata operations in a JuiceFS file system, such as creating files, deleting files, and renaming directory entries. It can be used for operation auditing, troubleshooting, building external consumers that follow metadata changes, and serving as a change source for incrementally syncing one file system to another.

This is a beta feature and requires JuiceFS v1.4.0 or later.

:::note
Changelog entries are stored in the metadata engine. This feature is disabled by default. After it is enabled, it adds extra write and storage overhead to the metadata engine.

The changelog records metadata operations and does not contain file data. However, it may still contain sensitive metadata, such as file names, extended attribute values, and delegation token operations. Treat changelog output as sensitive data.
:::

## Enable and Retention Policy {#enable-and-retention}

Use `juicefs config` to enable or disable the changelog:

```shell
juicefs config META-URL --changelog
juicefs config META-URL --changelog=false
```

When enabling the changelog, if `--changelog-max-age` is not explicitly set, JuiceFS sets the default retention time to 2 hours. Use `--changelog-max-age` and `--changelog-max-lines` to control the retention window:

```shell
juicefs config META-URL --changelog-max-age 2h --changelog-max-lines 1000000
```

Set `--changelog-max-age` or `--changelog-max-lines` to `0` to disable the corresponding cleanup rule. Changelog entries are cleaned up by client background tasks. For metadata-intensive workloads, set the retention policy carefully to avoid continuous metadata growth.

## Tail Changelog {#tail-changelog}

Use `juicefs changelog` to tail changelog entries:

```shell
juicefs changelog META-URL
```

When `--from` is not set, or is set to `0`, the command starts from the latest changelog version, waits for new entries, and keeps running until interrupted.

To resume consumption from a known version, pass the last processed version to `--from`:

```shell
juicefs changelog META-URL --from 100
```

External consumers should persist the latest fully processed version and pass it to `--from` after restart. For TiKV metadata engines, the command may output already processed entries because of the rewind window. Consumers need to deduplicate by changelog version or with business-side idempotency logic.

## Incremental Sync {#incremental-sync}

The changelog can serve as the change source for a custom incremental sync program.

### Recommended Workflow {#recommended-workflow}

1. Enable the changelog on the source file system and configure a large enough retention window to ensure entries are not cleaned up during the initial backup, load, or consumer interruption.
2. Create a metadata backup from the source file system and load the full backup into another file system. The backup records the latest changelog version at the time it is created.
3. Use the binary metadata backup format as the initial baseline when possible, because it provides better consistency and is better suited as the starting point for later changelog-based incremental sync.
4. Use the version recorded in the backup as the starting point, and start the consumer with `juicefs changelog SOURCE-META-URL --from VERSION`.
5. Convert each metadata operation from the output and apply it to the target file system. Persist the latest fully processed changelog version so the consumer can resume after restart.

### TiKV Rewind Window {#tikv-rewind-window}

If the source metadata engine is TiKV, note that the changelog version uses the transaction `startTs`, not the transaction commit time. A transaction may start before the metadata backup records the latest changelog version, but commit after the backup is created. Reading only from the version recorded in the backup may miss such entries.

To avoid missing entries, the consumer needs to read from a window before the version recorded in the backup and deduplicate entries that have already been applied. `juicefs changelog` performs this rewind internally for TiKV. The default TiKV rewind window is 10 seconds in TSO time, and can be adjusted with the `JFS_TKV_REWIND` environment variable.

TiKV metadata backups include changelog entries in this rewind window, so consumers can use those entries in the backup to build the initial deduplication set. When the same versions are later read from `juicefs changelog`, skip entries that have already been applied from the baseline backup.

## Output Format {#output-format}

Each line uses the following format:

```text
VERSION: UNIX_SECONDS.NANOSECONDS|OPERATION(arguments)[:result]|(SESSION_ID,TXN_ID)
```

- `VERSION`: changelog cursor.
- `UNIX_SECONDS.NANOSECONDS`: timestamp when the operation happened.
- `OPERATION(arguments)[:result]`: metadata operation and its internal arguments. Some operations append the result, such as a newly created inode, after `:`.
- `SESSION_ID`: JuiceFS client session ID that produced the entry.
- `TXN_ID`: transaction ID within that client session.

Example:

```text
101: 1716440752.123456789|CREATE(1,report.txt,1000,1000,1,420,18,,Keep,true):1024|(3,88)
102: 1716440753.000000000|WRITE(1024,0,0,233344,4096,1716440753,0):1|(3,89)
103: 1716440760.000000000|UNLINK(1,report.txt,0,false,true):1024|(3,90)
```

## Notes and Limitations {#notes}

- The changelog is not a metadata backup. Use [metadata backup](metadata_dump_load.md) for backup and restore.
- The changelog does not contain file data and cannot be used alone to restore files.
- If old entries have been cleaned up, `juicefs changelog --from` cannot recover the missing entries in between.
- Enabling the changelog increases metadata engine writes. For metadata-intensive workloads, evaluate the overhead before using a long retention window.
