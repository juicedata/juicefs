#!/usr/bin/env python3
"""Verify every file the writer recorded as durable is intact after a crash.

Only manifest entries are checked. Files the writer created but had not yet
fsync'd may be absent or short after a crash, which POSIX allows; a file the
filesystem said was durable may not be.
"""

import hashlib
import os
import sys


def main():
    if len(sys.argv) != 3:
        print("usage: crash_verify.py <mount_dir> <manifest>", file=sys.stderr)
        return 2
    mount_dir, manifest_path = sys.argv[1], sys.argv[2]
    data_dir = os.path.join(mount_dir, "crash_data")

    missing, corrupt, short, checked = [], [], [], 0
    with open(manifest_path) as manifest:
        for line in manifest:
            line = line.strip()
            if not line:
                continue
            name, want_sum, want_size = line.split()
            want_size = int(want_size)
            path = os.path.join(data_dir, name)
            checked += 1
            try:
                with open(path, "rb") as f:
                    data = f.read()
            except FileNotFoundError:
                missing.append(name)
                continue
            if len(data) != want_size:
                short.append("%s: %d bytes, expected %d" % (name, len(data), want_size))
            elif hashlib.sha256(data).hexdigest() != want_sum:
                corrupt.append(name)

    print("checked %d files recorded as durable" % checked)
    if checked == 0:
        print("<FATAL>: the manifest is empty, the writer never fsynced anything")
        return 1

    for label, items in (("MISSING", missing), ("TRUNCATED", short), ("CORRUPT", corrupt)):
        if items:
            print("<FATAL>: %d %s after the crash:" % (len(items), label))
            for item in items[:10]:
                print("  %s" % item)
            if len(items) > 10:
                print("  ... and %d more" % (len(items) - 10))

    if missing or short or corrupt:
        return 1
    print("all fsynced data survived the crash")
    return 0


if __name__ == "__main__":
    sys.exit(main())
