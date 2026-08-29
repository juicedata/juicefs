#!/usr/bin/env python3
"""Generate the golden Lance dataset fixture for lance-resolver tests.

The fixture is a REAL dataset written by the official Python Lance library
(pylance) -- never hand-crafted bytes. Usage:

    python gen-fixtures.py [output-dir]

Default output-dir is testdata/lance-dataset next to this script. After
regenerating the dataset, always regenerate the expectations too:

    python dump_fixture.py <output-dir> testdata/expected.json

Version pin: gen-protos.sh pins upstream Lance sources at v11.0.0-rc.1,
which is not published to crates.io or PyPI. pylance 10.0.0 is the closest
official writer available; the footer/CMO physical layout is shared across
Lance V2 file versions (verified against the v11 sources), so this fixture
is representative for the resolver. Data file names, transaction file names
and manifest timestamps are random/volatile by design; dump_fixture.py
normalizes them so expected.json stays stable across regenerations.

Do not bump the pinned versions casually: byte offsets in expected.json
depend on the exact writer behavior.
"""

import argparse
import pathlib
import shutil
import sys

PYLANCE_PIN = "10.0.0"
ROWS = 1000
ROWS_PER_FILE = 500  # two fragments / two data files


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", nargs="?", default=None, help="output dataset dir")
    args = parser.parse_args()

    import lance
    import pyarrow as pa

    writer_version = lance.__version__
    if writer_version != PYLANCE_PIN:
        print(
            f"error: pylance {PYLANCE_PIN} is pinned for fixtures, found {writer_version}; "
            "install it with: pip install pylance==" + PYLANCE_PIN,
            file=sys.stderr,
        )
        return 1

    out = pathlib.Path(args.output) if args.output else pathlib.Path(__file__).parent / "testdata" / "lance-dataset"
    if out.exists():
        shutil.rmtree(out)

    ids = pa.array(range(ROWS), type=pa.int64())
    names = pa.array([f"name-{i % 37}" for i in range(ROWS)], type=pa.string())
    cities = pa.array([f"city-{i % 11}" for i in range(ROWS)], type=pa.string())
    zips = pa.array([10000 + i % 97 for i in range(ROWS)], type=pa.int64())
    table = pa.table(
        {
            "id": ids,
            "name": names,
            # A struct column: non-leaf, --columns must fall back to full file.
            "addr": pa.StructArray.from_arrays([cities, zips], names=["city", "zip"]),
        }
    )
    ds = lance.write_dataset(table, str(out), max_rows_per_file=ROWS_PER_FILE)
    print(f"fixture written to {out} (pylance {writer_version})")
    for f in sorted(out.rglob("*")):
        if f.is_file():
            print(f"  {f.relative_to(out)}  ({f.stat().st_size} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
