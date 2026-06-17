---
title: Tiered Storage
sidebar_position: 8
description: Learn how to configure, migrate, and restore JuiceFS tiered storage.
---

Starting with JuiceFS 1.4, tiered storage lets you map individual files or directories to different object storage classes (Storage Classes). For example, keep hot data in Standard storage and move cold data to Infrequent Access (IA) or Glacier‑class storage to save costs.

## Key concepts

- **tier**: The tier identifier, ranging from `0` to `3`.
  - `0` is the default tier.
  - `1` to `3` are user‑configurable tiers.
- **storage-class**: The storage class assigned to a tier, for example, `STANDARD_IA`, `INTELLIGENT_TIERING`, or `GLACIER_IR`.
- **tag**: A custom object tag assigned to a tier, in the `key=value` format. It is attached to objects when they are uploaded, and can be used together with the cloud provider's lifecycle rules (see [Custom tags](#6-custom-tags)).
- **Tier attribute on a file/directory**: Stored in metadata; it determines which storage class is used for subsequent writes or object migrations.

## Prerequisites

1. The JuiceFS volume must be formatted and mounted.
2. The underlying object storage must support the target storage class and, if needed, the restoration of archived objects.
3. Define the tier mapping with `juicefs config` **before** running `juicefs tier set`.

## 1. Configure tier mappings

Assign a storage class to each tier (`1` to `3`):

```shell
juicefs config redis://localhost --tier 1 --storage-class STANDARD_IA -y
juicefs config redis://localhost --tier 2 --storage-class INTELLIGENT_TIERING -y
juicefs config redis://localhost --tier 3 --storage-class GLACIER_IR -y
```

List the current mappings:

```shell
juicefs tier list redis://localhost
```

## 2. Set a tier on a file or directory

### A single file

```shell
juicefs tier set redis://localhost --tier 1 /path/to/file
```

### A directory (non‑recursive, only the directory entry)

When you set a storage tier on a directory, any new files or subdirectories created inside it later will inherit the tier of the parent directory, automatically using the corresponding storage type.

```shell
juicefs tier set redis://localhost --tier 2 /path/to/dir
```

Without `-r`, only the directory inode is updated; files and subdirectories inside it are unchanged.

### A directory (recursive)

```shell
juicefs tier set redis://localhost --tier 2 /path/to/dir -r
```

Recursive mode processes all files and subdirectories under the target directory.

### Reset to the default tier (tier 0)

```shell
juicefs tier set redis://localhost --tier 0 /path/to/file
juicefs tier set redis://localhost --tier 0 /path/to/dir -r
```

## 3. Rewrite objects after a mapping change (`--force`)

If you change a tier's `storage-class` from A to B, the files' metadata tier‑id remains unchanged, but the objects in object storage are still stored as A.

Use `--force` to trigger a re-write, copying the objects to the new storage class:

```shell
juicefs tier set redis://localhost --tier 2 /path/to/dir -r --force
```

## 4. Restore archive objects

For archive storage classes such as `GLACIER` or `DEEP_ARCHIVE`, issue a restore request with:

```shell
juicefs tier restore redis://localhost /path/to/dir -r
```

`tier restore` only sends the restore request to the object storage service. Whether and when the objects become readable depends on the provider's restore duration. The active copy remains available for 3 days (default).

## 5. Checking tier status

Use `juicefs info` to inspect the tier information of a file:

```shell
juicefs info /mountpoint/path/to/file
```

Key fields to look for:

- `tier: <id>-><storage-class>` — the tier stored in metadata and its mapped storage class.
- `restore-status` — indicates whether the object is in an unfrozen state and when the active copy expires.
- `expected(...),actual(...)` — shown when the metadata mapping and the object's actual storage class differ. This signals that `tier set --force` is needed to rewrite the objects.
- `actual(...)` — shown for files with `tier=0`, displaying the object's actual storage class.

## 6. Custom tags

In addition to `storage-class`, you can assign a custom object tag to a tier, in the `key=value` format:

```shell
juicefs config redis://localhost --tier 1 --storage-class STANDARD --tag juicefs-tier=archive -y
```

You can also set a tag for the default tier (tier 0) when formatting:

```shell
juicefs format --storage-class STANDARD --tag juicefs-tier=archive redis://localhost myjfs
```

Once set, JuiceFS automatically attaches the tag to objects when they are uploaded. You can review the `tag` of each tier with `juicefs tier list`, or inspect a specific file's tag with `juicefs info`.

### Typical usage: tier down to archive with lifecycle rules

Uploading objects directly with an archive storage class (such as `GLACIER` or `DEEP_ARCHIVE`) can be expensive, because some cloud providers charge archive-tier write/request fees that make each upload API call costly. When the goal is to move data into an archive tier, a more economical approach is:

1. Keep uploading objects with a regular storage class such as `STANDARD`, and assign a custom tag to that tier, for example `--tag juicefs-tier=archive`.
2. Configure a lifecycle rule on the object storage bucket that filters objects by this tag and automatically transitions the matched objects to an archive storage class.

This avoids the high API costs of uploading archive-class objects directly, while still letting the cloud provider's lifecycle rules gradually move the data into the archive tier.

:::note
The tag must be in the `key=value` format (exactly one `=`, with a non-empty key and value); otherwise it is rejected or ignored. For how to configure lifecycle rules, refer to the documentation of your cloud provider.
:::

## Notes

- `tier set` only accepts file and directory paths.
- `--tier` only `0` to `3` are allowed.
- In writeback-cache mode (`--writeback`), `tier set` may fail if the file's data has not yet been uploaded to object storage. Wait for the upload to complete, then retry.
- Changing `--storage-class` does **not** automatically migrate existing objects. You must run `tier set ... --force` manually.
- `--tag` only takes effect when uploading new objects; it does not modify the tags of objects that already exist in object storage.
