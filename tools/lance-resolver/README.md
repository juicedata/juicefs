# lance-resolver

`lance-resolver` is a standalone CLI that resolves a
[Lance](https://lancedb.github.io/lance/) dataset into the exact set of files
that must be present to read a given dataset version, in a format that
`juicefs warmup -f` can consume.

JuiceFS stays format-agnostic: instead of teaching the filesystem about Lance's
on-disk layout, this tool does the format-specific parsing and emits a plain
list of paths (optionally with byte ranges) for warmup.

## Build

```bash
go build ./tools/lance-resolver
```

## Usage

```bash
lance-resolver [options] <dataset-path>
```

| Flag | Description |
|------|-------------|
| `--version VERSION` | Resolve a specific dataset version (default: latest) |
| `--manifest-only` | Output only the manifest file path |
| `--include-indices` | Also include index files under `_indices/` |
| `--columns COL1,COL2` | Column-level warmup for V2 data files (leaf columns only; metadata buffer ranges) |
| `--include-data-pages` | Include column page data buffer ranges (use with `--columns`) |
| `-h, --help` | Show help |

`--columns` supports only **leaf (primitive)** columns. Variable-length and
nested columns (`list`, `struct`, …) span multiple physical columns whose exact
layout is not described by the vendored protos, so requesting them falls back
to warming the **full data file** (with a warning) instead of risking an
incomplete warmup.

## Output format

Each line is either a bare path, or a path followed by a bracketed list of byte
ranges:

```
/path/to/dataset.lance/_versions/42.manifest
/path/to/dataset.lance/data/data_0.lance
/path/to/dataset.lance/data/data_0.lance [205600064-205823840;206605482-206633082]
```

`start` is inclusive and `end` is exclusive; ranges are `;`-separated. This
matches the `juicefs warmup -f` byte-range syntax.

## Examples

```bash
# Warm up every file of the latest version
lance-resolver /mnt/jfs/dataset.lance > /tmp/list.txt
juicefs warmup -f /tmp/list.txt

# Only the manifest
lance-resolver --manifest-only /mnt/jfs/dataset.lance

# Column-level warmup (metadata buffers only) for specific columns
lance-resolver --columns id,name /mnt/jfs/dataset.lance > /tmp/list.txt
juicefs warmup -f /tmp/list.txt
```

## Regenerating protobufs

The Lance protobuf definitions are vendored under `proto/`. To regenerate them
from a pinned Lance version:

```bash
./tools/lance-resolver/gen-protos.sh
```

Only the generated `*.pb.go` files are committed; the downloaded `.proto` files
are not.

## Future work

Precise column-level warmup for nested columns (`list`, `struct`, …) is not
implemented: such columns span multiple physical columns whose exact layout is
only described by Lance's full encoding model, which is not part of the vendored
protos. Reimplementing the encoding walker in pure Go would be fragile and drift
with upstream format changes.

If this becomes a requirement, the planned direction is to delegate the format
parsing to the official Rust `lance` / `lance-file` crates (for example via a
small sidecar binary), keeping this tool's output format unchanged.
