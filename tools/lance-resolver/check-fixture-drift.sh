#!/usr/bin/env bash
set -euo pipefail

# Detect Lance format drift: regenerate the golden fixture with the pinned
# official writer (pylance, see gen-fixtures.py) and verify that the
# normalized ground truth in testdata/expected.json is unchanged. A diff
# means the upstream Lance format (or our pin) moved and the resolver needs
# a second look -- run the golden tests and re-verify the layout facts
# against the new upstream sources before regenerating anything.
#
# Why diff the dumped JSON and not the fixture bytes: data file names and
# the manifest timestamp are random per run, and the writer permutes which
# content lands in which file. dump_fixture.py normalizes all of that, so
# the JSON is stable across regenerations (verified empirically).
#
# Usage: tools/lance-resolver/check-fixture-drift.sh
# Requires python3 with the pinned pylance installed:
#   pip install pylance==10.0.0

cd "$(dirname "$0")"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

python gen-fixtures.py "${TMP_DIR}/lance-dataset"
python dump_fixture.py "${TMP_DIR}/lance-dataset" "${TMP_DIR}/expected.json"

if git diff --no-index --exit-code testdata/expected.json "${TMP_DIR}/expected.json"; then
  echo "fixture ground truth unchanged"
else
  echo "ERROR: testdata/expected.json does not match the pinned writer output." >&2
  echo "If the drift is expected, re-verify the layout facts, then regenerate:" >&2
  echo "  python gen-fixtures.py && python dump_fixture.py testdata/lance-dataset testdata/expected.json" >&2
  exit 1
fi
