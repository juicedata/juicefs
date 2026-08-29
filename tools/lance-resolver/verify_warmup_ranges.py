#!/usr/bin/env python3
"""Adversarial verification that column-level warmup ranges are sufficient.

The official pylance reader serves as an independent judge (a "test oracle"
in software-testing terms — nothing to do with Oracle the database):

Independently proves that the ranges emitted by lance-resolver are
SUFFICIENT for the official reader: for each data file, every byte OUTSIDE
the emitted ranges is zeroed; the dataset copy is then read with the
official pylance reader. If the read returns the same data as the pristine
dataset, the official reader touched only warmed bytes — i.e. a JuiceFS
cache populated from the resolver output would have served the read
entirely.

This breaks self-confirmation: the verdict comes from the official
reader's behavior, not from lance-resolver or dump_fixture.py (which share
their layout knowledge with the Go implementation).

Notes:
  - Each case runs in its own process: a failing case panics a Rust
    background thread and poisons the pylance runtime for the rest.
  - pylance's to_table(columns=[...]) only accepts top-level names; nested
    sub-fields (e.g. "city" inside struct "addr") cannot be projected by
    the high-level API, so warm them via their parent.
  - Files are matched against the resolver output by their path relative
    to the dataset root (not by basename, so same-named files in different
    directories cannot collide). Ranges resolved to paths OUTSIDE the
    dataset (imported base paths) are reported and left untouched — the
    copy does not contain them. Use --multi-base to cover that case.
  - Requires Go unless LANCE_RESOLVER_BIN points at a prebuilt binary
    (built into a temp dir otherwise).

Coverage boundary (deliberate, synthetic tests cover these paths):
  - metadata-only ranges (--columns without --include-data-pages): pylance
    reads page data even for filters that match no rows (no statistics to
    prune with on this fixture), so a real-data oracle cannot isolate the
    metadata phase with the public API.
  - external row-id / row-version sequence files: the inline threshold is
    ~200KB of sequence data, far beyond what a fixture-sized dataset can
    produce through the public API.
  - indirect (deferred) encodings: the official reader rejects such files
    outright at this pin, so no readable real file can carry them.

Usage:
  python verify_warmup_ranges.py <dataset-dir> <col1,col2,...>
  python verify_warmup_ranges.py --multi-base    # generates a temp dataset
                                                # with an imported base and
                                                # verifies root + base files
"""

import os
import re
import shutil
import subprocess
import sys
import tempfile

import lance

TOOL_DIR = os.path.dirname(os.path.abspath(__file__))


def rel_key(path, dataset_abs):
    """Dataset-relative slash-normalized key for a resolver output path, or
    None when the path lives outside the dataset."""
    try:
        rel = os.path.relpath(os.path.abspath(path), dataset_abs)
    except ValueError:
        return None  # different drive (Windows)
    rel = rel.replace(os.sep, "/")
    if rel == ".." or rel.startswith("../"):
        return None
    return rel


def parse_ranges(output, dataset_abs=None):
    """Parse resolver output into {key: [(start, end), ...]}. With
    dataset_abs, keys are dataset-relative for files inside the dataset and
    absolute for external base paths; without it, all keys are absolute."""
    ranges = {}
    for line in output.splitlines():
        m = re.match(r"(.+) \[([\d;-]+)\]", line.strip())
        if not m:
            continue
        p = m.group(1)
        key = rel_key(p, dataset_abs) if dataset_abs else os.path.abspath(p)
        if key is None:
            print(f"note: {p} resolves outside the dataset; "
                  "its ranges are not verified by this run")
            continue
        ranges[key] = [tuple(map(int, r.split("-"))) for r in m.group(2).split(";")]
    return ranges


def zero_outside(file_path, ranges):
    """Zero every byte of file_path outside the given ranges; returns the
    number of zeroed bytes."""
    data = bytearray(open(file_path, "rb").read())
    covered = bytearray(len(data))
    for a, b in ranges:
        for i in range(a, min(b, len(data))):
            covered[i] = 1
    zeroed = 0
    for i in range(len(data)):
        if not covered[i]:
            data[i] = 0
            zeroed += 1
    open(file_path, "wb").write(bytes(data))
    return zeroed


def build_binary(tmp):
    """Return the resolver binary path. Set LANCE_RESOLVER_BIN to a prebuilt
    binary to skip the build (CI builds once and reuses it across cases;
    each case still runs in its own process)."""
    prebuilt = os.environ.get("LANCE_RESOLVER_BIN")
    if prebuilt:
        if not os.path.isfile(prebuilt):
            raise SystemExit(f"LANCE_RESOLVER_BIN={prebuilt} does not exist")
        return prebuilt
    exe = ".exe" if os.name == "nt" else ""
    binary = os.path.join(tmp, "bin", "lance-resolver" + exe)
    subprocess.run(["go", "build", "-o", binary, "."], cwd=TOOL_DIR, check=True)
    return binary


def verify_multi_base(tmp) -> bool:
    """Generate a temp dataset whose files are split between the dataset
    root and an imported base (file:// URI), then adversarially verify the
    --columns ranges over BOTH, zeroing in place (everything is under tmp)."""
    import pyarrow as pa
    from lance.dataset import DatasetBasePath

    binary = build_binary(tmp)
    root = os.path.join(tmp, "root")
    aux = os.path.join(tmp, "aux").replace(os.sep, "/")
    # padding stays UNprojected so the id ranges leave real data cold —
    # a zero-byte adversarial pass would prove nothing.
    table = pa.table({
        "id": pa.array(range(200), pa.int64()),
        "padding": pa.array([f"{i % 7}-" + "x" * 60 for i in range(200)], pa.string()),
    })
    aux_uri = "file:///" + aux if re.match(r"^[A-Za-z]:", aux) else "file://" + aux
    base = DatasetBasePath(path=aux_uri, name="aux", is_dataset_root=False)
    lance.write_dataset(
        table, root, initial_bases=[base], target_all_bases=True, max_rows_per_file=50
    )

    out = subprocess.run(
        [binary, "--columns", "id", "--include-data-pages", root],
        cwd=TOOL_DIR, capture_output=True, text=True, check=True,
    ).stdout
    ranges = parse_ranges(out)
    base_files = [k for k in ranges if "aux" in k.replace(os.sep, "/")]
    if not ranges or not base_files:
        print("FAIL: expected ranges for both dataset and external base files")
        return False

    want = lance.dataset(root).to_table(columns=["id"]).to_pydict()
    zeroed = sum(zero_outside(p, rs) for p, rs in ranges.items())
    if zeroed == 0:
        print("FAIL: nothing was zeroed — the adversarial pass did not bite")
        return False
    got = lance.dataset(root).to_table(columns=["id"]).to_pydict()
    if want != got:
        print(f"FAIL: data mismatch after zeroing {zeroed} bytes outside ranges "
              f"({len(base_files)} base files of {len(ranges)})")
        return False
    print(f"PASS: official reader served --columns id across dataset root and "
          f"imported base ({len(ranges)} files, {len(base_files)} in base, "
          f"{zeroed} bytes outside ranges zeroed)")
    return True


def main() -> int:
    if len(sys.argv) == 2 and sys.argv[1] == "--multi-base":
        with tempfile.TemporaryDirectory(prefix="lance_oracle_mb_") as tmp:
            return 0 if verify_multi_base(tmp) else 1

    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2
    dataset = os.path.abspath(sys.argv[1])
    columns = sys.argv[2]

    with tempfile.TemporaryDirectory(prefix="lance_oracle_") as tmp:
        binary = build_binary(tmp)
        out = subprocess.run(
            [binary, "--columns", columns, "--include-data-pages", dataset],
            cwd=TOOL_DIR, capture_output=True, text=True, check=True,
        ).stdout
        ranges = parse_ranges(out, dataset)
        if not ranges:
            print("resolver emitted no usable ranges (full-file fallback?)")
            return 1

        dst = os.path.join(tmp, "copy")
        shutil.copytree(dataset, dst)

        zeroed = 0
        for root_dir, _, files in os.walk(dst):
            for f in files:
                fp = os.path.join(root_dir, f)
                key = os.path.relpath(fp, dst).replace(os.sep, "/")
                if key in ranges:
                    zeroed += zero_outside(fp, ranges[key])

        want = lance.dataset(dataset).to_table(columns=columns.split(",")).to_pydict()
        got = lance.dataset(dst).to_table(columns=columns.split(",")).to_pydict()
        if want != got:
            print(f"FAIL: data mismatch after zeroing {zeroed} bytes outside ranges")
            return 1
        print(f"PASS: official reader served --columns {columns} with "
              f"{zeroed} bytes outside the emitted ranges zeroed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
