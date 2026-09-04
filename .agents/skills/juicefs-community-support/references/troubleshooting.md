# Troubleshooting and Recovery

## Frame the incident

Record before proposing a fix:

- exact symptom and first observed time;
- JuiceFS client, CSI Driver, image, metadata engine, object storage, and Kubernetes versions as applicable;
- topology, access path, and affected clients, nodes, Pods, filesystems, or workloads;
- blast radius and business impact;
- recent changes;
- last known good state and available metadata or infrastructure backups.

Use the smallest evidence set that can distinguish the likely layers. Do not ask for broad unredacted archives by default.

## Collect read-only evidence first

Depending on the path, collect:

- client version, process state, mount state, filesystem identity, status, and information commands documented for that release;
- metrics, access logs, client logs, and kernel or FUSE errors;
- metadata engine health, latency, connections, capacity, and backup status;
- object storage authentication, throttling, timeout, capacity, and request errors;
- cache capacity, permissions, eviction, disk health, and resource pressure;
- Kubernetes events and CSI Controller, CSI Node, Mount Pod, sidecar, and workload logs.

Redact metadata credentials, database passwords, object storage keys, Secret values, encryption material, kubeconfig content, and customer data.

## Route by layer

- **Installation or startup:** binary, architecture, dependencies, permissions, service manager, flags, and configuration.
- **Metadata:** reachability, authentication, latency, capacity, sessions, locks, transactions, backup, and filesystem identity.
- **Object storage:** endpoint, region, credentials, bucket or prefix, throttling, lifecycle policy, consistency, and request failures.
- **Mount or I/O:** application, kernel, FUSE, VFS, chunk I/O, local cache, network, metadata, and object storage.
- **CSI:** provisioning, scheduling, node publish, mount lifecycle, propagation, workload I/O, and cleanup.
- **Performance:** workload shape, concurrency, file sizes, cache state, client resources, metadata latency, object latency, network, and throttling.

Read the matching public source when an error path, retry, timeout, option precedence, cleanup path, or suspected regression remains unclear. Do not use development-branch code as proof for the deployed release.

## Preserve evidence and data

Do not use cleanup, mass restart, credential rotation, reformatting, metadata import, object deletion, cache deletion, or PV or PVC deletion as an initial test.

If there is suspected data loss, corruption, wrong bucket or prefix, filesystem UUID mismatch, unexpected metadata replacement, or widespread I/O failure:

1. Stop avoidable writes and automation that may change state.
2. Preserve logs, versions, configurations, filesystem identity, and time ranges.
3. Verify backups and recovery targets without overwriting the affected system.
4. Escalate before repair, import, deletion, or destructive garbage collection.

## Propose bounded remediation

Each change should state:

- evidence and hypothesis;
- exact target and command or configuration field;
- expected result and failure mode;
- prerequisites and backup;
- rollback or recovery procedure;
- verification signal and observation window;
- required approval.

Change one variable at a time whenever possible. Preserve before-and-after evidence and distinguish temporary mitigation from root-cause correction.

## Recover on a controlled target

Use recovery procedures that match the deployed version and metadata engine. Confirm source backup, destination, filesystem identity, object storage mapping, credentials, and expected downtime before proceeding.

Avoid running competing metadata services or clients against an ambiguous restore target. After recovery, perform end-to-end read and write validation, cross-client checks, metrics review, and an explicit decision on how the old environment will be isolated or retired.

## Close with evidence

Report:

1. Verified symptom and impact.
2. Evidence collected and sensitive fields removed.
3. Confirmed or most likely failing layer.
4. Documentation and exact source revision used.
5. Actions performed and approvals received.
6. End-to-end acceptance result.
7. Remaining risk, monitoring window, and follow-up owner.
