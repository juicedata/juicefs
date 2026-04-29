---
title: Tiered Storage
sidebar_position: 8
description: Learn how to configure, migrate, and restore JuiceFS tiered storage.
---

JuiceFS supports tiered storage since v1.4, letting you map individual files or directories to different object storage classes (Storage Classes), for example keeping hot data in Standard storage while moving cold data to IA or Glacier-class storage to reduce costs.

## Key Concepts

- **tier-id**: The tier identifier, in the range `0–3`.
  - `0` is the default tier (reserved value).
  - `1–3` are user-configurable tiers.
- **tier-sc**: The storage class assigned to a tier-id (e.g. `STANDARD_IA`, `INTELLIGENT_TIERING`, `GLACIER_IR`).
- **Tier attribute on a file/directory**: Stored in metadata; determines which storage class is used for subsequent writes or object migrations.

## Prerequisites

1. The JuiceFS volume has already been formatted and mounted.
2. The underlying object storage supports the target storage class and, if needed, archive-object restore.
3. Define the tier mapping with `juicefs config` **before** running `juicefs tier set`.

## 1. Configure Tier Mappings

Assign a storage class to each tier (1–3):

```shell
juicefs config redis://localhost --tier-id 1 --tier-sc STANDARD_IA -y
juicefs config redis://localhost --tier-id 2 --tier-sc INTELLIGENT_TIERING -y
juicefs config redis://localhost --tier-id 3 --tier-sc GLACIER_IR -y
```

List the current mappings:

```shell
juicefs tier list redis://localhost
```

`id=0` is always shown as `default`.

## 2. Set a Tier on a File or Directory

### Single file

```shell
juicefs tier set redis://localhost --id 1 /path/to/file
```

### Directory (directory entry only, non-recursive)

The purpose of setting a storage tier for a directory itself is that when new files or subdirectories are created in this directory later, they will inherit the tier-id of their parent directory, thereby automatically using the corresponding storage type.

```shell
juicefs tier set redis://localhost --id 2 /path/to/dir
```

Without `-r`, only the directory inode itself is updated; files and sub-directories inside are not changed.

### Directory (recursive)

```shell
juicefs tier set redis://localhost --id 2 /path/to/dir -r
```

Recursive mode processes all files and sub-directories under the target directory.

### Reset to the default tier (tier 0)

```shell
juicefs tier set redis://localhost --id 0 /path/to/file
juicefs tier set redis://localhost --id 0 /path/to/dir -r
```

## 3. Re-writing Objects after a Mapping Change (`--force`)

If you change a tier-id's `tier-sc` from A to B, the files' metadata tier-id stays the same, but the objects in object storage are still stored as A.  
Use `--force` to trigger a re-write, copying the objects to the new storage class:

```shell
juicefs tier set redis://localhost --id 2 /path/to/dir -r --force
```

## 4. Restoring Archive Objects

For archive storage classes such as `GLACIER` or `DEEP_ARCHIVE`, issue a restore request with:

```shell
juicefs tier restore redis://localhost /path/to/dir -r
```

`tier restore` only sends the restore request to the object storage service. Whether and when the objects become readable depends on the object storage provider's restore duration. Lifetime of the active copy in days is 3 days

## 5. Checking Tier Status

Use `juicefs info` to inspect the tier information of a file:

```shell
juicefs info /mountpoint/path/to/file
```

Key fields to look for:

- `tier: <id>-><storage-class>` — the tier-id stored in metadata and its mapped storage class.
- `restore-status`：display whether the object is in an unfrozen state and the expiration time of the copy.
- `expected(...),actual(...)` — shown when the metadata mapping and the object's real storage class differ. This signals that `tier set --force` is needed to re-write the objects.
- `actual(...)` — shown for `tier-id=0` files, displaying the object's actual storage class.

## Notes

1. `tier set` only accepts file and directory paths.
2. `--id` accepts values `0–3`; when configuring with `--tier-id`, only `1–3` are accepted.
3. In writeback-cache mode (`--writeback`), `tier set` may fail if the file's data has not yet been uploaded to object storage. Wait for the upload to complete, then retry.
4. Changing `--tier-sc` does **not** automatically migrate existing objects. You must run `tier set ... --force` manually.
