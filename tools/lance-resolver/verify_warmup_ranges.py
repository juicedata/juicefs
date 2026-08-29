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

This breaks self-confirmation: the verdict comes from the official reader's behavior, not from lance-resolver or dump_fixture.py
(which share their layout knowledge with the Go implementation).

Notes:
  - Each case runs in its own process: a failing case panics a Rust
    background thread and poisons the pylance runtime for the rest.
  - pylance's to_table(columns=[...]) only accepts top-level names; nested
    sub-fields (e.g. "city" inside struct "addr") cannot be projected by
    the high-level API, so warm them via their parent.
  - Requires Go (the resolver binary is built into a temp dir).

Usage: python verify_warmup_ranges.py <dataset-dir> <col1,col2,...>
"""

import os
import re
import shutil
import subprocess
import sys
import tempfile

import lance

TOOL_DIR = os.path.dirname(os.path.abspath(__file__))


def resolver_ranges(dataset, columns):
    # Build into a temp dir so the binary never lands in the source tree
    # (a stray .exe here has been committed by mistake once already).
    exe = ".exe" if os.name == "nt" else ""
    binary = os.path.join(tempfile.mkdtemp(prefix="lance_oracle_bin_"), "lance-resolver" + exe)
    subprocess.run(
        ["go", "build", "-o", binary, "."], cwd=TOOL_DIR, check=True,
    )
    out = subprocess.run(
        [binary, "--columns", columns, "--include-data-pages", dataset],
        cwd=TOOL_DIR, capture_output=True, text=True, check=True,
    )
    ranges = {}
    for line in out.stdout.splitlines():
        m = re.match(r"(.+) \[([\d;-]+)\]", line.strip())
        if m:
            ranges[os.path.basename(m.group(1))] = [
                tuple(map(int, r.split("-"))) for r in m.group(2).split(";")
            ]
    if not ranges:
        raise SystemExit("resolver emitted no ranges (full-file fallback)")
    return ranges


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2
    dataset = os.path.abspath(sys.argv[1])
    columns = sys.argv[2]

    ranges = resolver_ranges(dataset, columns)
    dst = os.path.join(tempfile.mkdtemp(prefix="lance_oracle_"), "copy")
    shutil.copytree(dataset, dst)

    zeroed = 0
    for root, _, files in os.walk(dst):
        for f in files:
            if f not in ranges:
                continue
            fp = os.path.join(root, f)
            data = bytearray(open(fp, "rb").read())
            covered = bytearray(len(data))
            for a, b in ranges[f]:
                for i in range(a, min(b, len(data))):
                    covered[i] = 1
            for i in range(len(data)):
                if not covered[i]:
                    data[i] = 0
                    zeroed += 1
            open(fp, "wb").write(bytes(data))

    want = lance.dataset(dataset).to_table(columns=columns.split(",")).to_pydict()
    got = lance.dataset(dst).to_table(columns=columns.split(",")).to_pydict()
    if want != got:
        print(f"FAIL: data mismatch after zeroing {zeroed} bytes outside ranges")
        return 1
    print(f"PASS: official reader served --columns {columns} with "
          f"{zeroed} bytes outside the emitted ranges zeroed")
    shutil.rmtree(dst, ignore_errors=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
