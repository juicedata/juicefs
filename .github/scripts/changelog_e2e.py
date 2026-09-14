#!/usr/bin/env python3
"""Run all nine Docker/FUSE changelog replication combinations.

Usage: python3 .github/scripts/changelog_e2e.py [--output NEW_DIRECTORY]
Requires Python 3.9+, Git, and local Docker with /dev/fuse support.
Builds the current working tree, including uncommitted changes, inside Docker.
Results and logs survive cleanup. Exit 0 means all nine cases and cleanup passed;
exit 1 means a test/setup/cleanup failure; exit 130 means interrupted.
See changelog_e2e.md for details and limitations.
"""

import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tarfile
import tempfile
import time
import traceback
import uuid

CLI = '/work/juicefs'
WORKLOAD = '/src/.github/scripts/changelog_workload.py'
ENGINES = ['redis', 'tikv', 'mysql']
RESULTS = []


def event(s):
    print(s, flush=True)


def inside(args):
    return ['docker', 'exec', RUNNER] + [str(a) for a in args]


def run(args, log, timeout=300, required=True):
    with open(log, 'w') as f:
        with subprocess.Popen(args, stdout=f, stderr=subprocess.STDOUT) as p:
            deadline = time.monotonic() + timeout
            try:
                while True:
                    try:
                        p.wait(timeout=min(30, max(.01, deadline - time.monotonic())))
                        break
                    except subprocess.TimeoutExpired:
                        if time.monotonic() >= deadline:
                            raise TimeoutError(f'command timed out: {log}')
                        event(f'[wait] {Path(log).name}')
            except BaseException:
                p.kill()
                p.wait()
                raise
    if p.returncode and required:
        raise RuntimeError(f'exit {p.returncode}: {log}')
    return p.returncode


def background(args, log, pid):
    f = open(log, 'w')
    p = subprocess.Popen(inside(['sh', '-c', 'echo $$ > "$1"; shift; exec "$@"', 'sh', pid] + args), stdout=f, stderr=subprocess.STDOUT)
    f.close()
    return p


def uri(engine, source, is_source=False):
    suffix = source + ('_src' if is_source else '_dst')
    if engine == 'redis':
        return 'redis://redis:6379/' + str(0 if is_source else 1 + ENGINES.index(source))
    if engine == 'tikv':
        return 'tikv://pd:2379/e2e_' + suffix
    return 'mysql://root:e2e-local@(mysql:3306)/e2e_' + suffix


def progress(folder):
    try:
        lines = (folder / 'progress.jsonl').read_text().splitlines()
        for line in reversed(lines):
            try:
                return json.loads(line)
            except json.JSONDecodeError:
                pass
    except FileNotFoundError:
        pass
    return {'reads': 0, 'writes': 0, 'cycles': 0}


def mount(meta, point, log, readonly):
    subprocess.run(inside(['mkdir', '-p', point]), check=True)
    pid = '/work/' + str(log.relative_to(ROOT)) + '.pid'
    args = ['env', 'JFS_SUPERVISOR=test', CLI, 'mount', '-f', '--no-bgjob', '--no-agent', '--max-deletes', '0', '--no-usage-report', '--heartbeat', '1s', '--cache-dir', 'memory', '--cache-size', '0', '--metrics', '127.0.0.1:0', '--attr-cache', '0', '--entry-cache', '0', '--dir-entry-cache', '0', '--atime-mode', 'strictatime']
    if readonly:
        args += ['--read-only']
    p = background(args + [meta, point], log, pid)
    for _ in range(120):
        if p.poll() is not None:
            raise RuntimeError(f'mount exited: {log}')
        r = subprocess.run(inside(['mountpoint', '-q', point]), capture_output=True)
        if r.returncode == 0:
            return p
        time.sleep(.25)
    raise RuntimeError(f'mount timeout: {log}')


def unmount(point, proc, log):
    run(inside(['umount', point]), log, timeout=40)
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        pid = next(str(a) for a in proc.args if str(a).endswith('.log.pid'))
        run(inside(['sh', '-c', 'kill -TERM "$(cat "$1")"', 'sh', pid]), str(log) + '.signal', required=False)
        proc.wait(timeout=40)


def stop_apply(proc, pid, log):
    if proc.poll() is not None:
        return {'exit': proc.returncode, 'forced_after_catchup': False}
    run(inside(['sh', '-c', 'kill -INT "$(cat "$1")"', 'sh', pid]), log, required=False)
    try:
        proc.wait(timeout=10)
        return {'exit': proc.returncode, 'forced_after_catchup': False}
    except subprocess.TimeoutExpired:
        run(inside(['sh', '-c', 'kill -KILL "$(cat "$1")"', 'sh', pid]), log, required=False)
        proc.wait(timeout=10)
        return {'exit': proc.returncode, 'forced_after_catchup': True}


def redis_snapshot(folder):
    run(['docker', 'exec', PREFIX + '-redis', 'redis-cli', 'BGSAVE'], folder / 'bgsave.log')
    for _ in range(300):
        info = subprocess.check_output(['docker', 'exec', PREFIX + '-redis', 'redis-cli', 'INFO', 'persistence'], text=True)
        if 'rdb_bgsave_in_progress:0' in info:
            assert 'rdb_last_bgsave_status:ok' in info
            break
        time.sleep(.1)
    else:
        raise RuntimeError('BGSAVE timeout')
    run(['docker', 'cp', PREFIX + '-redis:/data/dump.rdb', str(folder / 'dump.rdb')], folder / 'copy-rdb.log')
    run(['docker', 'run', '-d', '--name', PREFIX + '-snapshot', '--label', 'com.juicefs.changelog-e2e=' + PREFIX, '--network', PREFIX, '--network-alias', 'snapshot', '-v', str(folder) + ':/snapshot:ro', 'redis:7-alpine', 'redis-server', '--dir', '/snapshot', '--dbfilename', 'dump.rdb', '--save', '', '--appendonly', 'no'], folder / 'snapshot-redis.log')
    for _ in range(100):
        r = subprocess.run(['docker', 'exec', PREFIX + '-snapshot', 'redis-cli', 'PING'], capture_output=True)
        if b'PONG' in r.stdout:
            return 'redis://snapshot:6379/0'
        time.sleep(.1)
    raise RuntimeError('snapshot Redis did not start')


def compare(source, destination):
    a, b = json.loads(source.read_text()), json.loads(destination.read_text())
    ae, be = a['entries'], b['entries']
    diff = {'missing': sorted(ae.keys() - be.keys()), 'extra': sorted(be.keys() - ae.keys()), 'content': [], 'metadata': [], 'timestamps': [], 'read_errors': a['errors'] + b['errors'], 'source_statfs': a['statfs'], 'destination_statfs': b['statfs']}
    for path in sorted(ae.keys() & be.keys()):
        for field in ae[path].keys() | be[path].keys():
            x, y = ae[path].get(field), be[path].get(field)
            if x != y:
                group = 'content' if field in ['sha256', 'target', 'xattrs'] else 'timestamps' if field.endswith('_ns') else 'metadata'
                diff[group].append({'path': path, 'field': field, 'source': x, 'destination': y})
    diff['statfs_equal'] = a['statfs'] == b['statfs']
    diff['source_entries'], diff['destination_entries'] = len(ae), len(be)
    diff['source_files'] = sum('sha256' in e for e in ae.values())
    diff['pass'] = not any(diff[k] for k in ['missing', 'extra', 'content', 'metadata', 'timestamps', 'read_errors']) and diff['statfs_equal']
    return diff


def round_trip(engine):
    folder = ROOT / engine
    folder.mkdir()
    inner = '/work/' + engine
    src = uri(engine, engine, True)
    targets = {e: uri(e, engine) for e in ENGINES}
    mounts, appliers, timeline, outcomes = {}, {}, [], {e: {'source': engine, 'destination': e, 'status': 'NOT RUN'} for e in ENGINES}
    writer = None
    source_point = '/mnt/' + engine + '-source'
    phase = 'prepare'
    try:
        event(f'[{engine}] Preparing 1200 files and starting concurrent I/O')
        run(inside([CLI, 'format', '--storage', 'file', '--bucket', '/objects/' + engine, '--block-size', '64', '--trash-days', '0', '--enable-acl', src, 'changelog-e2e-' + engine]), folder / 'format.log')
        run(inside([CLI, 'config', src, '--changelog', '--changelog-max-age', '1h']), folder / 'config.log')
        mounts[source_point] = mount(src, source_point, folder / 'mount-source.log', False)
        run(inside(['python3', WORKLOAD, 'prime', source_point]), folder / 'prime.log', timeout=600)
        writer = background(['python3', WORKLOAD, 'live', source_point, inner], folder / 'live.log', inner + '/writer.pid')
        time.sleep(2)
        if writer.poll() is not None:
            raise RuntimeError('source workload exited before backup')
        phase = 'dump'
        stage = {'stage': 'dump', 'before': progress(folder), 'start': time.time()}
        event(f'[{engine}] Dumping baseline with concurrent I/O')
        dump_src = redis_snapshot(folder) if engine == 'redis' else src
        backup = inner + '/baseline.bin'
        run(inside([CLI, 'dump', '--binary', '--threads', '1', dump_src, backup]), folder / 'dump.log')
        stage.update(after=progress(folder), end=time.time())
        timeline.append(stage)
        event(f'[{engine}] Loading baselines and starting apply')
        for dest, url in targets.items():
            phase = 'load'
            outcomes[dest]['status'] = 'FAIL'
            stage = {'stage': 'load->' + dest, 'before': progress(folder), 'start': time.time()}
            rc = run(inside([CLI, 'load', '--binary', '--threads', '1', url, backup]), folder / f'load-{dest}.log', required=False)
            stage.update(after=progress(folder), end=time.time(), exit=rc)
            timeline.append(stage)
            outcomes[dest]['load_exit'] = rc
            if rc == 0:
                phase = 'apply'
                pid = inner + f'/apply-{dest}.pid'
                appliers[dest] = background([CLI, 'changelog', 'apply', src, url, '--backup', backup], folder / f'apply-{dest}.log', pid)
                point = '/mnt/' + engine + '-' + dest
                mounts[point] = mount(url, point, folder / f'mount-{dest}.log', True)
        phase = 'workload'
        stage = {'stage': 'online_apply', 'before': progress(folder), 'start': time.time()}
        for _ in range(OPTIONS.duration):
            if writer.poll() is not None:
                break
            time.sleep(1)
        stage.update(after=progress(folder), end=time.time())
        timeline.append(stage)
        (folder / 'stop').touch()
        writer_rc = writer.wait(timeout=90)
        event(f'[{engine}] Writers stopped; waiting for catch-up')
        if writer_rc:
            raise RuntimeError('source workload failed: ' + str(folder / 'live.log'))
        marker = engine + '-finished-' + PREFIX
        run(inside(['python3', WORKLOAD, 'seal', source_point, marker]), folder / 'seal.log')
        phase = 'apply'
        # All writer descriptors are closed and fsynced. Keep the idle source mounted
        # until verification; metadata statistics continue their normal periodic flush.
        pending, deadline = set(appliers), time.monotonic() + OPTIONS.catchup_timeout
        next_progress = time.monotonic() + 30
        while pending and time.monotonic() < deadline:
            for dest in list(pending):
                if appliers[dest].poll() is not None:
                    outcomes[dest]['apply_failed_exit'] = appliers[dest].returncode
                    pending.remove(dest)
                    continue
                probe = subprocess.run(inside(['python3', WORKLOAD, 'probe', '/mnt/' + engine + '-' + dest]), capture_output=True, text=True, timeout=20)
                if probe.returncode == 0 and probe.stdout.strip() == marker:
                    outcomes[dest]['marker_reached'] = True
                    pending.remove(dest)
            if pending:
                if time.monotonic() >= next_progress:
                    event(f'[{engine}] Waiting for catch-up: {", ".join(sorted(pending))}')
                    next_progress = time.monotonic() + 30
                time.sleep(1)
        for dest in pending:
            outcomes[dest]['catchup_timeout'] = True
        # Allow post-close cleanup/statistics entries to drain after the marker.
        time.sleep(12)
        run(inside([CLI, 'changelog', src, '--from', '1', '--follow=false']), folder / 'source-changelog.log')
        for dest, proc in appliers.items():
            outcomes[dest]['apply_shutdown'] = stop_apply(proc, inner + f'/apply-{dest}.pid', folder / f'stop-apply-{dest}.log')
        phase = 'compare'
        # Fresh read-only mounts prevent writer or apply-time attribute caches from affecting comparison.
        for point in list(mounts):
            if point == source_point:
                continue
            unmount(point, mounts.pop(point), folder / f'unmount-{Path(point).name}.log')
        event(f'[{engine}] Comparing file content and metadata on fresh read-only mounts')
        urls = {'source': src, **{e: targets[e] for e in appliers}}
        for dest, url in urls.items():
            point = '/mnt/' + engine + '-' + ('verify-source' if dest == 'source' else dest)
            mounts[point] = mount(url, point, folder / f'remount-{dest}.log', True)
        def snapshot(dest):
            return run(inside(['python3', WORKLOAD, 'snapshot', '/mnt/' + engine + '-' + ('verify-source' if dest == 'source' else dest), inner + '/' + dest + '.json']), folder / f'snapshot-{dest}.log', required=False, timeout=300)
        with concurrent.futures.ThreadPoolExecutor(4) as pool:
            snapshot_exit = dict(zip(urls, pool.map(snapshot, urls)))
        for dest in ENGINES:
            o = outcomes[dest]
            o['traffic'] = progress(folder)
            o['writer_exit'] = writer_rc
            o['snapshot_exit'] = snapshot_exit.get(dest)
            o['source_snapshot_exit'] = snapshot_exit.get('source')
            if dest in appliers and (folder / 'source.json').exists() and (folder / f'{dest}.json').exists():
                diff = compare(folder / 'source.json', folder / f'{dest}.json')
                (folder / f'diff-{dest}.json').write_text(json.dumps(diff, indent=2))
                o.update({k: diff[k] for k in ['statfs_equal', 'source_statfs', 'destination_statfs', 'source_entries', 'destination_entries', 'source_files']})
                for k in ['missing', 'extra', 'content', 'metadata', 'timestamps', 'read_errors']:
                    o[k] = len(diff[k])
                o['pass'] = diff['pass'] and o.get('marker_reached', False) and 'apply_failed_exit' not in o and o.get('apply_shutdown', {}).get('exit') == 0 and not o.get('apply_shutdown', {}).get('forced_after_catchup') and snapshot_exit.get(dest) == 0 and snapshot_exit.get('source') == 0
            else:
                o['pass'] = False
    except (Exception, KeyboardInterrupt) as e:
        error = traceback.format_exc()
        (folder / 'error.log').write_text(error)
        event(f'[{engine}] {phase}: {type(e).__name__}: {str(e).splitlines()[0] if str(e) else "interrupted"}; log: {folder / "error.log"}')
        for o in outcomes.values():
            o['pass'] = False
            o['round_error'] = error
            o['failure_stage'] = phase
        if isinstance(e, KeyboardInterrupt):
            raise
    finally:
        if writer is not None and writer.poll() is None:
            (folder / 'stop').touch()
            try:
                writer.wait(timeout=60)
            except subprocess.TimeoutExpired:
                run(inside(['sh', '-c', 'kill -KILL "$(cat "$1")"', 'sh', inner + '/writer.pid']), folder / 'stop-writer.log', required=False)
        for dest, proc in appliers.items():
            if proc.poll() is None:
                try:
                    stop_apply(proc, inner + f'/apply-{dest}.pid', folder / f'stop-apply-{dest}.log')
                except Exception as e:
                    with (folder / 'cleanup-errors.log').open('a') as log:
                        log.write(traceback.format_exc())
                    event(f'[cleanup] {e}; log: {folder / "cleanup-errors.log"}')
        for point, proc in mounts.items():
            try:
                unmount(point, proc, folder / f'cleanup-{Path(point).name}.log')
            except Exception as e:
                with (folder / 'cleanup-errors.log').open('a') as log:
                    log.write(traceback.format_exc())
                event(f'[cleanup] {e}; log: {folder / "cleanup-errors.log"}')
        for stage in timeline:
            stage['read_delta'] = stage['after']['reads'] - stage['before']['reads']
            stage['write_delta'] = stage['after']['writes'] - stage['before']['writes']
        (folder / 'timeline.json').write_text(json.dumps(timeline, indent=2))
        for o in outcomes.values():
            relevant = [s for s in timeline if s['stage'] in ['dump', 'online_apply', 'load->' + o['destination']]]
            o['continuous_io_verified'] = len(relevant) == 3 and all(s['read_delta'] > 0 and s['write_delta'] > 0 for s in relevant)
            o['pass'] = o.get('pass', False) and o['continuous_io_verified']
            if o['pass']:
                o['status'] = 'PASS'
            print_result(o)
            RESULTS.append(o)
        (ROOT / 'results.json').write_text(json.dumps(RESULTS, indent=2))


def service(name, image, options, args):
    run(['docker', 'run', '-d', '--name', PREFIX + '-' + name,
         '--label', 'com.juicefs.changelog-e2e=' + PREFIX, '--network', PREFIX,
         '--network-alias', name] + options + [image] + args, ROOT / f'start-{name}.log')


def setup():
    event('[setup] Snapshotting the current working tree')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=REPO, text=True).strip()
    (ROOT / 'revision.txt').write_text(revision + '\n')
    (ROOT / 'source.patch').write_bytes(subprocess.check_output(['git', 'diff', 'HEAD', '--binary'], cwd=REPO))
    names = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=REPO).split(b'\0')
    manifest = {}
    with tarfile.open(ROOT / 'source.tar', 'w') as archive:
        for name in sorted(set(os.fsdecode(n) for n in names if n)):
            path = REPO / name
            if ROOT == path or ROOT in path.parents or not path.exists():
                continue
            archive.add(path, arcname=name, recursive=False)
            if path.is_file():
                manifest[name] = hashlib.sha256(path.read_bytes()).hexdigest()
    (ROOT / 'source-manifest.json').write_text(json.dumps(manifest, indent=2))
    (ROOT / 'pd.toml').write_text('[replication]\nmax-replicas = 1\n')
    (ROOT / 'tikv.toml').write_text(
        '[storage]\nreserve-space = "1GB"\n'
        '[storage.block-cache]\ncapacity = "256MB"\n'
        '[raftstore]\nstore-pool-size = 2\napply-pool-size = 2\n'
        '[readpool.unified]\nmin-thread-count = 1\nmax-thread-count = 2\n')
    event('[setup] Starting metadata services')
    run(['docker', 'network', 'create', '--label', 'com.juicefs.changelog-e2e=' + PREFIX, PREFIX], ROOT / 'network.log')
    service('redis', 'redis:7-alpine', [], ['redis-server', '--save', '', '--appendonly', 'no'])
    service('mysql', 'mysql:8.4', ['-e', 'MYSQL_ROOT_PASSWORD=e2e-local', '-e', 'MYSQL_ROOT_HOST=%'], [])
    service('pd', 'pingcap/pd:v8.5.7', ['-v', str(ROOT / 'pd.toml') + ':/config.toml:ro'],
            ['--name=pd', '--data-dir=/data', '--client-urls=http://0.0.0.0:2379',
             '--advertise-client-urls=http://pd:2379', '--peer-urls=http://0.0.0.0:2380',
             '--advertise-peer-urls=http://pd:2380', '--initial-cluster=pd=http://pd:2380', '--config=/config.toml'])
    service('tikv', 'pingcap/tikv:v8.5.7', ['-v', str(ROOT / 'tikv.toml') + ':/config.toml:ro'],
            ['--addr=0.0.0.0:20160', '--advertise-addr=tikv:20160', '--pd=pd:2379', '--data-dir=/data', '--config=/config.toml'])
    service('runner', 'golang:1.25-bookworm',
            ['--device', '/dev/fuse', '--cap-add', 'SYS_ADMIN', '--security-opt', 'apparmor=unconfined',
             '-v', str(ROOT) + ':/work'], ['sleep', 'infinity'])
    event('[setup] Installing FUSE tools')
    run(inside(['sh', '-ec', 'sed -i s,http://deb.debian.org,https://deb.debian.org,g /etc/apt/sources.list.d/debian.sources; '
                'apt-get update -qq; apt-get install -y -qq fuse3 acl python3 curl; '
                'mkdir -p /src /objects; tar -xf /work/source.tar -C /src']), ROOT / 'packages.log', timeout=600)
    if OPTIONS.binary:
        event(f'[build] Using supplied binary: {OPTIONS.binary}')
        shutil.copyfile(OPTIONS.binary, ROOT / 'juicefs')
        run(inside(['chmod', '+x', CLI]), ROOT / 'binary-permissions.log')
    else:
        event('[build] Building juicefs')
        run(['docker', 'exec', '-w', '/src', RUNNER, 'go', 'build', '-mod=readonly', '-o', CLI, '.'], ROOT / 'build.log', timeout=1800)
    (ROOT / 'binary-sha256.txt').write_text(hashlib.sha256((ROOT / 'juicefs').read_bytes()).hexdigest() + '\n')
    run(inside([CLI, 'version']), ROOT / 'binary-version.log')
    run(inside([CLI, 'changelog', 'apply', '--help']), ROOT / 'apply-help.log')
    # A listening PD port alone does not mean TiKV has an Up store yet.
    for _ in range(180):
        mysql = subprocess.run(['docker', 'exec', PREFIX + '-mysql', 'mysqladmin', '-uroot', '-pe2e-local', 'ping'], capture_output=True, timeout=10)
        redis = subprocess.run(['docker', 'exec', PREFIX + '-redis', 'redis-cli', 'PING'], capture_output=True, timeout=10)
        pd = subprocess.run(inside(['curl', '-fsS', '--max-time', '5', 'http://pd:2379/pd/api/v1/stores']), capture_output=True, timeout=10)
        if pd.returncode == 0:
            (ROOT / 'tikv-readiness.json').write_bytes(pd.stdout)
            stores = json.loads(pd.stdout).get('stores', [])
            if mysql.returncode == 0 and b'PONG' in redis.stdout and any(s['store']['state_name'] == 'Up' for s in stores):
                break
        time.sleep(1)
    else:
        raise RuntimeError('metadata services did not become ready within 180 seconds')
    sql = '\n'.join(f'CREATE DATABASE e2e_{s}_{suffix};' for s in ENGINES for suffix in ('src', 'dst'))
    run(['docker', 'exec', PREFIX + '-mysql', 'mysql', '-uroot', '-pe2e-local', '-e', sql], ROOT / 'mysql-databases.log')


def cleanup():
    event('[cleanup] Removing mounts, containers, volumes and network')
    report = {'prefix': PREFIX, 'remaining_containers': [], 'remaining_volumes': [], 'errors': []}
    names = [PREFIX + '-' + n for n in ('snapshot', 'runner', 'tikv', 'pd', 'mysql', 'redis')]
    volumes = set()
    (ROOT / 'docker-logs').mkdir(exist_ok=True)
    for name in names:
        try:
            info = subprocess.run(['docker', 'inspect', name], capture_output=True, text=True, timeout=30)
            if info.returncode == 0:
                volumes.update(m['Name'] for m in json.loads(info.stdout)[0]['Mounts'] if m['Type'] == 'volume')
                run(['docker', 'logs', '--tail', '3000', name], ROOT / 'docker-logs' / f'{name}.log', required=False)
        except Exception as e:
            report['errors'].append(str(e))
    try:
        run(inside(['sh', '-c', 'findmnt -rn -t fuse.juicefs -o TARGET | while IFS= read -r point; do umount -l "$point"; done']), ROOT / 'cleanup-mounts.log', timeout=30, required=False)
    except Exception as e:
        report['errors'].append(str(e))
    for name in names:
        try:
            run(['docker', 'rm', '-f', '-v', name], ROOT / f'cleanup-{name}.log', timeout=90, required=False)
        except Exception as e:
            report['errors'].append(str(e))
    try:
        network = subprocess.run(['docker', 'network', 'inspect', PREFIX], capture_output=True, text=True, timeout=30)
        if network.returncode == 0:
            for endpoint in json.loads(network.stdout)[0].get('Containers', {}).values():
                if endpoint['Name'] in names:
                    run(['docker', 'network', 'disconnect', '-f', PREFIX, endpoint['Name']], ROOT / 'cleanup-disconnect.log', required=False)
            for _ in range(5):
                if run(['docker', 'network', 'rm', PREFIX], ROOT / 'cleanup-network.log', required=False) == 0:
                    break
                time.sleep(1)
        existing = set(subprocess.check_output(['docker', 'ps', '-a', '--format', '{{.Names}}'], text=True, timeout=30).splitlines())
        remaining_volumes = set(subprocess.check_output(['docker', 'volume', 'ls', '-q'], text=True, timeout=30).splitlines())
        networks = set(subprocess.check_output(['docker', 'network', 'ls', '--format', '{{.Name}}'], text=True, timeout=30).splitlines())
        report.update(remaining_containers=sorted(existing.intersection(names)), remaining_volumes=sorted(remaining_volumes.intersection(volumes)), network_remaining=PREFIX in networks)
    except Exception as e:
        report['errors'].append(str(e))
    for path in [ROOT / 'source.tar', ROOT / 'juicefs'] + list(ROOT.glob('*/baseline.bin')) + list(ROOT.glob('*/dump.rdb')) + list(ROOT.glob('*/*.pid')) + list(ROOT.glob('*/stop')):
        path.unlink(missing_ok=True)
    report['complete'] = not report['remaining_containers'] and not report['remaining_volumes'] and not report.get('network_remaining', True) and not report['errors']
    (ROOT / 'cleanup.json').write_text(json.dumps(report, indent=2))
    return report['complete']


def print_result(row):
    source, dest = row['source'], row['destination']
    folder = ROOT / source
    log = folder / f'apply-{dest}.log'
    if log.exists():
        with log.open(errors='replace') as f:
            row['apply_error'] = next((line.strip() for line in f if 'Failed to apply changelog:' in line), '')
    event(f'{row["status"]} {source:<5} -> {dest}')
    if row['status'] != 'FAIL':
        return
    stage, error = 'compare', ''
    if row.get('round_error'):
        stage, error, log = row['failure_stage'], row['round_error'].strip().splitlines()[-1], folder / 'error.log'
    elif row.get('load_exit') != 0:
        stage, error, log = 'load', f'exit {row.get("load_exit")}', folder / f'load-{dest}.log'
    elif row.get('apply_error') or 'apply_failed_exit' in row or row.get('apply_shutdown', {}).get('exit') != 0 or row.get('apply_shutdown', {}).get('forced_after_catchup'):
        stage = 'apply'
        error = row.get('apply_error') or f'exit {row.get("apply_failed_exit", row.get("apply_shutdown", {}).get("exit"))}'
        if row.get('apply_shutdown', {}).get('forced_after_catchup'):
            error += '; forced shutdown'
    elif row.get('catchup_timeout') or not row.get('marker_reached'):
        stage, error = 'catch-up', 'end marker not reached'
    elif row.get('source_snapshot_exit') != 0:
        error, log = f'source snapshot exit {row.get("source_snapshot_exit")}', folder / 'snapshot-source.log'
    elif row.get('snapshot_exit') != 0:
        error, log = f'destination snapshot exit {row.get("snapshot_exit")}', folder / f'snapshot-{dest}.log'
    elif not row['continuous_io_verified']:
        stage, error, log = 'continuous-io', 'reads/writes did not increase during every phase', folder / 'timeline.json'
    event(f'     stage: {stage}')
    if error:
        event(f'     error: {error}')
        event(f'     log: {log}')
    for field in ['missing', 'extra', 'content', 'metadata', 'timestamps', 'read_errors']:
        if row.get(field):
            event(f'     {field}: {row[field]} mismatches')
    if row.get('statfs_equal') is False:
        event('     statfs: mismatch')
    diff = folder / f'diff-{dest}.json'
    if diff.exists():
        event(f'     diff: {diff}')


def summarize(error, cleaned):
    by_pair = {(r['source'], r['destination']): r for r in RESULTS}
    rows = []
    for source in ENGINES:
        for dest in ENGINES:
            row = by_pair.get((source, dest))
            if row is None:
                row = {'source': source, 'destination': dest, 'pass': False, 'status': 'NOT RUN', 'round_error': error or 'not executed'}
                print_result(row)
            rows.append(row)
    (ROOT / 'results.json').write_text(json.dumps(rows, indent=2))
    if error:
        (ROOT / 'error.log').write_text(error)
        event(f'[error] {error.strip().splitlines()[-1]}; log: {ROOT / "error.log"}')
    if not cleaned and (ROOT / 'cleanup.json').exists():
        cleanup_result = json.loads((ROOT / 'cleanup.json').read_text())
        for field in ['remaining_containers', 'remaining_volumes']:
            if cleanup_result[field]:
                event(f'[cleanup] {field}: {", ".join(cleanup_result[field])}')
        if cleanup_result.get('network_remaining'):
            event(f'[cleanup] remaining_network: {PREFIX}')
        if cleanup_result['errors']:
            event(f'[cleanup] {cleanup_result["errors"][0].splitlines()[0]}')
        event(f'[cleanup] Details: {ROOT / "cleanup.json"}')
    passed = sum(r['status'] == 'PASS' for r in rows)
    failed = sum(r['status'] == 'FAIL' for r in rows)
    not_run = sum(r['status'] == 'NOT RUN' for r in rows)
    event(f'\n{passed} passed, {failed} failed, {not_run} not run | cleanup: {"OK" if cleaned else "FAIL"}')
    event(f'Artifacts: {ROOT}')
    return passed == 9 and cleaned and not error


def main():
    global ROOT, PREFIX, RUNNER, REPO, OPTIONS
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--output', type=Path, help='new directory for results and logs (default: a new temporary directory)')
    parser.add_argument('--duration', type=int, default=30, help='seconds of concurrent I/O after all appliers start (default: 30)')
    parser.add_argument('--catchup-timeout', type=int, default=600, help='seconds to wait for destinations after writers stop (default: 600)')
    parser.add_argument('--binary', type=Path, help='use an existing Linux juicefs binary matching the Docker architecture instead of building')
    OPTIONS = parser.parse_args()
    if OPTIONS.duration < 1:
        parser.error('--duration must be positive')
    if OPTIONS.catchup_timeout < 1:
        parser.error('--catchup-timeout must be positive')
    if OPTIONS.binary and not OPTIONS.binary.is_file():
        parser.error('--binary must point to an existing Linux executable')
    REPO = Path(__file__).resolve().parents[2]
    if OPTIONS.output:
        ROOT = OPTIONS.output.expanduser().resolve()
        ROOT.mkdir(parents=True, exist_ok=False)
    else:
        ROOT = Path(tempfile.mkdtemp(prefix='juicefs-changelog-e2e-')).resolve()
    PREFIX = 'jfs-changelog-e2e-' + uuid.uuid4().hex[:12]
    RUNNER = PREFIX + '-runner'
    (ROOT / 'prefix.txt').write_text(PREFIX + '\n')
    error, interrupted, cleaned = '', False, False
    def interrupt(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupt)
    try:
        run(['docker', 'info'], ROOT / 'docker-info.log', timeout=30)
        setup()
        for engine in ENGINES:
            round_trip(engine)
    except KeyboardInterrupt:
        error, interrupted = 'Interrupted; remaining cases were not executed.', True
    except Exception:
        error = traceback.format_exc()
    finally:
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        try:
            cleaned = cleanup()
        except Exception:
            error += '\nCleanup error: ' + traceback.format_exc()
        success = summarize(error, cleaned)
    return 130 if interrupted else 0 if success else 1


if __name__ == '__main__':
    sys.exit(main())
