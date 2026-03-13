---
title: Path Protection
description: Learn how to use path protection to restrict access to specific directories in JuiceFS.
sidebar_position: 3
---

Path protection is a feature that allows you to restrict access to specific paths in JuiceFS using regular expression patterns. This is useful for protecting sensitive directories like `.git` from accidental modifications, or completely hiding secret directories.

## Protection Modes

Two protection modes are supported:

| Mode | Write Operations | Read Operations |
|------|------------------|-----------------|
| `readonly` | Blocked | Allowed |
| `deny` | Blocked | Blocked |

## Usage

Enable path protection using the `--path-protection` option when mounting:

### Readonly Mode

Protect `.git` directories from write operations while allowing reads:

```shell
juicefs mount \
  --path-protection='{"rules":[{"pattern":"^/mnt/jfs/data/.*\\.git.*","mode":"readonly"}]}' \
  redis://localhost /mnt/jfs
```

### Deny Mode

Completely block access to secrets directory:

```shell
juicefs mount \
  --path-protection='{"rules":[{"pattern":"^/mnt/jfs/secrets/.*","mode":"deny"}]}' \
  redis://localhost /mnt/jfs
```

### Multiple Rules

You can specify multiple protection rules:

```shell
juicefs mount \
  --path-protection='{"rules":[{"pattern":"^/mnt/jfs/data/.*\\.git.*","mode":"readonly"},{"pattern":"^/mnt/jfs/secrets/.*","mode":"deny"}]}' \
  redis://localhost /mnt/jfs
```

## Pattern Matching

Patterns are regular expressions that match against the full path including the mountpoint. It is recommended to use `^` to anchor patterns to the beginning of the path to avoid unintended substring matches:

- Pattern: `^/mnt/jfs/data/.*\.git.*` matches `/mnt/jfs/data/project/.git/config`
- Pattern: `^/mnt/jfs/secrets/.*` matches `/mnt/jfs/secrets/api_key.txt`
- Pattern without `^`: `/mnt/jfs/secrets/.*` would also match `/backup/mnt/jfs/secrets/foo` (unintended)

## Affected Operations

### Write Protection (readonly and deny modes)

- Create file/directory
- Write to file
- Delete file/directory
- Rename file/directory
- Create symbolic/hard links
- Modify attributes
- Set/remove extended attributes

### Read Protection (deny mode only)

- Read file content
- Lookup file/directory
- List directory contents
- Get file attributes
- Get extended attributes

## Error Codes

| Error | Code | Description |
|-------|------|-------------|
| `EPERM` | 1 | Operation not permitted (write operations on protected paths) |
| `EACCES` | 13 | Permission denied (read operations on deny-protected paths) |

## Examples

```shell
# Test write protection (should fail)
touch /mnt/jfs/data/project/.git/test.txt
# Output: touch: cannot touch '...': Operation not permitted

# Test read allowed in readonly mode (should succeed)
cat /mnt/jfs/data/project/.git/config
# Output: success

# Test read blocked in deny mode (should fail)
cat /mnt/jfs/secrets/api_key.txt
# Output: cat: ...: Permission denied

# Test lookup blocked in deny mode (should fail)
ls /mnt/jfs/secrets/
# Output: ls: cannot access ...: Permission denied
```

## Notes

1. Path protection rules are evaluated in order. The first matching rule determines the protection mode.
2. Patterns must match the full path including the mountpoint prefix. Use `^` to anchor patterns.
3. Invalid regex patterns will cause the mount to fail with an error message.
