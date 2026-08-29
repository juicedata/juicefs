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

`--columns` supports leaf (primitive) columns as well as **nested `struct` and
`list` columns**: nested parents have no physical column of their own (their
data, including list offsets, lives inside the descendant leaf columns), so a
nested request expands to the byte ranges of every column in its subtree —
verified against real fixtures. Columns using *deferred (indirect) encodings*
still fall back to the **full data file** (with a warning): their encoding
buffers live outside the column metadata and no reference implementation
defines how `buffer_location` resolves (the upstream v11 reader rejects such
files), so guessing ranges would risk incomplete warmup.

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

Data files are resolved through the manifest's `base_paths` when they carry a
`base_id` (shallow clones and imported files may live outside the dataset
directory): dataset-root bases keep files under `data/`, direct-file bases use
the base path as-is, and the base paths must point into the JuiceFS mount for
warmup to reach them. Deletion files always resolve against the dataset root,
mirroring upstream.

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

Both generators are pinned for reproducible output: `protoc-gen-go` is
installed via `go install` at a fixed version, and a matching `protoc` release
binary is downloaded from the protobuf releases (override with
`PROTOC_VERSION`/`PROTOC_GEN_GO_VERSION`/`LANCE_VERSION`). No protoc
installation is required on the host. Only the generated `*.pb.go` files are
committed; the downloaded `.proto` files are not.

## Testing and fixtures

Format correctness is proven by golden tests against a **real** Lance dataset
committed under `testdata/` — written by the official `pylance` (pinned in
`gen-fixtures.py`), never hand-crafted bytes. The expected values in
`testdata/expected.json` are extracted from the fixture bytes by the
independent parser `dump_fixture.py`, which also normalizes volatile values
(random file names, manifest timestamp, per-run file content permutation) so
the JSON is stable across regenerations.

```bash
pip install pylance==10.0.0
python tools/lance-resolver/gen-fixtures.py                    # regenerate testdata/lance-dataset
python tools/lance-resolver/dump_fixture.py \
    tools/lance-resolver/testdata/lance-dataset \
    tools/lance-resolver/testdata/expected.json               # regenerate ground truth
go test ./tools/lance-resolver/...
```

`check-fixture-drift.sh` regenerates into a temp dir and diffs the normalized
ground truth against the committed `expected.json` (`git diff --exit-code`),
failing when the pinned writer's format has drifted; CI runs it on every change
under `tools/lance-resolver/` (`.github/workflows/lance-resolver-fixture-drift.yml`).

## Future work

Deferred (indirect) column encodings are detected but not range-resolved (see
the note above): if a reference implementation for `buffer_location` lands
upstream, the detection can turn into proper ranges.

If deeper format introspection is ever needed (encodings, statistics, …),
the planned direction is to delegate the format parsing to the official Rust
`lance` / `lance-file` crates (for example via a small sidecar binary),
keeping this tool's output format unchanged.
