#!/usr/bin/env python3
"""Compare root FUSE cleanup paths on disposable Linux CI runners."""

import ctypes
import datetime
import errno
import os
from pathlib import Path
import shlex
import shutil
import stat
import subprocess


def run(command, check=False):
    print(f"$ {shlex.join(command)}", flush=True)
    result = subprocess.run(command, capture_output=True, text=True, timeout=20)
    print(result.stdout, end="")
    print(result.stderr, end="")
    print(f"exit={result.returncode}", flush=True)
    if check:
        result.check_returncode()
    return result.returncode


def mounts(mountpoint):
    return [line for line in Path("/proc/self/mountinfo").read_text().splitlines()
            if line.split()[4] == mountpoint]


def make_stale(mountpoint):
    Path(mountpoint).mkdir(exist_ok=True)
    fd = os.open("/dev/fuse", os.O_RDWR)
    try:
        options = (f"fd={fd},rootmode={stat.S_IFDIR:o},user_id=0,group_id=0,"
                   "max_read=131072,allow_other")
        libc = ctypes.CDLL(None, use_errno=True)
        libc.mount.argtypes = [ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p,
                               ctypes.c_ulong, ctypes.c_char_p]
        libc.mount.restype = ctypes.c_int
        # Match go-fuse's root DirectMount path: MS_NOSUID | MS_NODEV.
        if libc.mount(b"JuiceFS:stale-probe", os.fsencode(mountpoint),
                      b"fuse.juicefs", 6, options.encode()) != 0:
            error = ctypes.get_errno()
            raise OSError(error, os.strerror(error))
    finally:
        # Closing the only connection leaves a real, disconnected FUSE mount.
        os.close(fd)
    try:
        os.stat(mountpoint)
    except OSError as error:
        if error.errno == errno.ENOTCONN:
            print(f"confirmed ENOTCONN at {mountpoint}: {mounts(mountpoint)}")
            return
        raise
    raise RuntimeError(f"expected ENOTCONN at {mountpoint}")


def probe(mode, label, command, mountpoint):
    print(f"\n=== mode={mode} command={label} mountpoint={mountpoint} ===")
    if mounts(mountpoint):
        raise RuntimeError(f"refusing to replace an existing mount at {mountpoint}")
    try:
        make_stale(mountpoint)
        code = run(command + [mountpoint])
        remaining = mounts(mountpoint)
        print(f"remaining_mounts={remaining}")
        results.append((mode, label, mountpoint, code, len(remaining)))
        if (label == "direct" or mode == "allow-umount") and (code != 0 or remaining):
            raise RuntimeError("expected unmount to detach the mount")
    finally:
        if mounts(mountpoint):
            run(["umount", "--internal-only", "--no-canonicalize", "--lazy",
                 mountpoint], check=True)
        if mounts(mountpoint):
            raise RuntimeError(f"cleanup left a mount at {mountpoint}")


if __name__ == "__main__":
    if os.geteuid() != 0:
        raise SystemExit("run as root on a disposable Linux runner")
    started_at = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    print(Path("/etc/os-release").read_text())
    run(["uname", "-a"])
    print(f"PATH={os.environ.get('PATH')}")
    print(f"process_profile={Path('/proc/self/attr/current').read_text().strip()}")
    run(["dpkg-query", "-W", "fuse3", "apparmor", "mount"])
    for name in ("fusermount", "fusermount3", "umount"):
        binary = shutil.which(name)
        print(f"{name}: path={binary}, realpath={os.path.realpath(binary) if binary else None}")
        if binary:
            run([binary, "-V"])

    profile = Path("/etc/apparmor.d/fusermount3")
    local_profile = Path("/etc/apparmor.d/local/fusermount3")
    original_local = local_profile.read_text() if local_profile.exists() else ""
    profiles = Path("/sys/kernel/security/apparmor/profiles")
    loaded = profiles.read_text() if profiles.exists() else ""
    print(f"loaded_fuse_profiles={[line for line in loaded.splitlines() if 'fuse' in line]}")
    if profile.exists():
        print(profile.read_text())
    commands = [
        ("juicefs-choice", ["fusermount", "-uz"] if shutil.which("fusermount")
         else ["umount", "-l"]),
        ("distro-fusermount3", ["/usr/bin/fusermount3", "-uz"]),
        ("direct", ["umount", "--internal-only", "--no-canonicalize", "--lazy"]),
    ]
    results = []
    changed_mode = False
    changed_local = False
    try:
        for mode in ("original", "allow-umount", "complain"):
            if mode != "original":
                if not profile.exists() or "fusermount3 (enforce)" not in loaded:
                    print("skip policy comparisons: fusermount3 is not enforced")
                    break
            if mode == "allow-umount":
                local_profile.parent.mkdir(exist_ok=True)
                changed_local = True
                local_profile.write_text(original_local + "\n# JuiceFS CI\numount,\n")
                run(["apparmor_parser", "-r", str(profile)], check=True)
                if "fusermount3 (enforce)" not in profiles.read_text():
                    raise RuntimeError("fusermount3 profile is not enforced")
                print("confirmed fusermount3 remains enforced with umount permission")
            elif mode == "complain":
                local_profile.write_text(original_local)
                # Reload only this profile; restore enforcement in finally.
                changed_mode = True
                run(["apparmor_parser", "-C", "-r", str(profile)], check=True)
            for mountpoint in ("/jfs", "/tmp/juicefs-stale-probe", "/var/tmp/juicefs-stale-probe"):
                for label, command in commands:
                    probe(mode, label, command, mountpoint)
    finally:
        try:
            if changed_local:
                local_profile.write_text(original_local)
            if changed_local or changed_mode:
                run(["apparmor_parser", "-r", str(profile)], check=True)
        finally:
            run(["journalctl", "-k", "--since", started_at, "--no-pager"])
            print("\nmode\tcommand\tmountpoint\texit\tremaining mounts")
            for result in results:
                print("\t".join(map(str, result)))
