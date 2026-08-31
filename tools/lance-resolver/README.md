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
| `--columns COL1,COL2` | Column-level warmup for V2 data files (leaf, `struct` and `list` columns; see below) |
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

Emitted ranges are conservatively merged across small gaps: readers coalesce
adjacent buffer reads over the alignment padding between buffers, and Lance
aligns buffers to 64-byte boundaries, so gaps of at most 64 bytes are warmed
too. Larger gaps between columns are left cold, keeping multi-column warmup
column-scoped; note that projecting most of a dataset's columns naturally
approaches warming the whole file. Range sufficiency is verified by
`scripts/verify_warmup_ranges.py`, which zeroes every byte outside the emitted
ranges and checks that the official reader still returns correct data —
including a `--multi-base` mode for datasets that keep files in imported base paths
(`file://` bases are emitted as plain local paths; other schemes pass
through, since those bases are not warmable through a local mount).
Metadata-only ranges, external row-id sequence files and indirect encodings
stay covered by synthetic tests only: the public pylance API cannot isolate
those reads (it touches page data even for filters matching no rows, and the
row-id inline threshold exceeds fixture scale), and the official reader
rejects indirect encodings outright.

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
warmup to reach them. A deletion file with a `base_id` resolves against that
base's dataset root and fails resolution if the base is missing or is not a
dataset-root base.

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

## Maintenance scripts

All maintenance and verification scripts live under `scripts/`:

| Script | Purpose |
|--------|---------|
| `scripts/gen-protos.sh` | Downloads the pinned Lance protobuf sources and pinned `protoc` toolchain, then regenerates the committed Go protobuf files. |
| `scripts/gen-fixtures.py` | Generates the real golden Lance dataset with the pinned official `pylance` writer. |
| `scripts/dump_fixture.py` | Independently parses fixture bytes and writes the normalized `testdata/expected.json` ground truth. |
| `scripts/check-fixture-drift.sh` | Regenerates a temporary fixture and fails if its normalized ground truth differs from the committed expectation. |
| `scripts/verify_warmup_ranges.py` | Zeroes bytes outside emitted ranges and verifies projected reads with the official Lance reader, including imported-base coverage. |

## Regenerating protobufs

The Lance protobuf definitions are vendored under `proto/`. To regenerate them
from a pinned Lance version:

```bash
./tools/lance-resolver/scripts/gen-protos.sh
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
`scripts/gen-fixtures.py`), never hand-crafted bytes. The expected values in
`testdata/expected.json` are extracted from the fixture bytes by the
independent parser `scripts/dump_fixture.py`, which also normalizes volatile
values (random file names, manifest timestamp, per-run file content
permutation) so the JSON is stable across regenerations.

```bash
pip install pylance==10.0.0
python tools/lance-resolver/scripts/gen-fixtures.py                    # regenerate testdata/lance-dataset
python tools/lance-resolver/scripts/dump_fixture.py \
    tools/lance-resolver/testdata/lance-dataset \
    tools/lance-resolver/testdata/expected.json               # regenerate ground truth
go test ./tools/lance-resolver/...
```

`scripts/check-fixture-drift.sh` regenerates into a temp dir and diffs the
normalized ground truth against the committed `expected.json` (`git diff
--exit-code`), failing when the pinned writer's format has drifted; CI runs it
on every change under `tools/lance-resolver/`
(`.github/workflows/lance-resolver-fixture-drift.yml`) together with the golden
tests and the adversarial range verification
(`scripts/verify_warmup_ranges.py`) for leaf, nested and multi-column
projections.

## TODO

- Resolve `--include-indices` from the selected manifest's `IndexSection`,
  including `IndexMetadata.base_id`, instead of scanning only the current
  dataset root's `_indices/` directory.
- Validate column-range parsing against official V2.2 and V2.3 writer fixtures,
  then add footer versions `(2,2)` and `(2,3)` to the supported whitelist.

## Future work

Deferred (indirect) column encodings are detected but not range-resolved (see
the note above): if a reference implementation for `buffer_location` lands
upstream, the detection can turn into proper ranges.

If deeper format introspection is ever needed (encodings, statistics, …),
the planned direction is to delegate the format parsing to the official Rust
`lance` / `lance-file` crates (for example via a small sidecar binary),
keeping this tool's output format unchanged.
