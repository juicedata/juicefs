#!/usr/bin/env python3
"""Write files into a JuiceFS mount, recording each one only after the
filesystem promised it was durable.

A file is appended to the manifest only after fsync() on the file and fsync()
on its parent directory both returned, so every manifest entry is a promise
POSIX requires to survive a crash. The manifest lives outside the mount (on the
runner's own disk) and is fsync'd itself, so killing the mount cannot lose it.

Runs until the mount is killed underneath it, which is the point: whatever made
it into the manifest has to be readable after the remount.
"""

import hashlib
import os
import sys
import time


def content_for(index, size):
    """Deterministic bytes for a file, so a mismatch is obvious in a diff."""
    seed = f"jfs-crash-{index}".encode()
    out = bytearray()
    block = seed
    while len(out) < size:
        block = hashlib.sha256(block).digest()
        out.extend(block)
    return bytes(out[:size])


def main():
    if len(sys.argv) != 5:
        print("usage: crash_writer.py <mount_dir> <manifest> <count> <size>", file=sys.stderr)
        return 2
    mount_dir, manifest_path, count, size = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])

    data_dir = os.path.join(mount_dir, "crash_data")
    os.makedirs(data_dir, exist_ok=True)
    # make the data directory's own entry durable before anything is recorded,
    # so losing it could never be blamed on the filesystem
    root_fd = os.open(mount_dir, os.O_RDONLY)
    try:
        os.fsync(root_fd)
    finally:
        os.close(root_fd)
    dir_fd = os.open(data_dir, os.O_RDONLY)

    written = 0
    with open(manifest_path, "w") as manifest:
        for i in range(count):
            name = "f_%06d" % i
            data = content_for(i, size)
            path = os.path.join(data_dir, name)
            try:
                fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o644)
                try:
                    view = memoryview(data)
                    while view:
                        # a short write would leave a file whose contents do not
                        # match the hash recorded below
                        view = view[os.write(fd, view):]
                    os.fsync(fd)  # the file's data
                finally:
                    os.close(fd)
                os.fsync(dir_fd)  # the directory entry
            except OSError as e:
                # the mount was killed under us: everything already recorded
                # still has to survive, so stop cleanly and let the verifier run
                print("writer stopping at %s: %s" % (name, e), flush=True)
                break

            manifest.write("%s %s %d\n" % (name, hashlib.sha256(data).hexdigest(), size))
            manifest.flush()
            os.fsync(manifest.fileno())
            written += 1
            if written % 200 == 0:
                print("fsynced %d files" % written, flush=True)

    try:
        os.close(dir_fd)  # already gone if the mount died under us
    except OSError:
        pass
    print("writer done, %d files recorded as durable" % written, flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
