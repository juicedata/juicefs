# Community Deployment and Operations

## Start from the current public documentation

- [Community Edition introduction](https://juicefs.com/docs/community/introduction)
- [Installation](https://juicefs.com/docs/community/getting-started/installation)
- [Standalone mode](https://juicefs.com/docs/community/getting-started/standalone)
- [Distributed mode](https://juicefs.com/docs/community/getting-started/for_distributed)
- [Production deployment recommendations](https://juicefs.com/docs/community/production_deployment_recommendations)
- [Metadata engines](https://juicefs.com/docs/community/databases_for_metadata)
- [Object storage](https://juicefs.com/docs/community/reference/how_to_set_up_object_storage)
- [Command reference](https://juicefs.com/docs/community/command_reference)
- [Status check and maintenance](https://juicefs.com/docs/community/administration/status_check_and_maintenance)
- [Metadata backup and restore](https://juicefs.com/docs/community/metadata_dump_load)
- [Monitoring](https://juicefs.com/docs/community/administration/monitoring)
- [Upgrade](https://juicefs.com/docs/community/administration/upgrade)

Confirm that the documentation applies to the deployed release. If a page describes a newer version, say so and verify the behavior against the matching tag.

If the public site is unavailable, ask for the local Community repository or documentation root plus its release tag, commit, or download date. Use `<JUICEFS_ROOT>/docs/en/` for English or `<JUICEFS_ROOT>/docs/zh_cn/` for Chinese, and verify that the supplied revision matches the deployed client before treating it as version evidence.

## Establish the topology

### Standalone mode

Standalone mode can use a local metadata database such as SQLite. Treat it as a local, single-file metadata topology rather than a shared distributed metadata service. Confirm where the metadata file resides, who backs it up, and whether more than one client is expected.

### Distributed mode

For multiple clients or production workloads, identify the network metadata engine and verify:

- high availability and backup ownership;
- latency and connection limits;
- authentication and transport security;
- supported server and JuiceFS client versions;
- restore testing and recovery objectives.

Keep these components separate:

- the metadata engine stores filesystem metadata;
- object storage holds file data chunks;
- clients provide filesystem and gateway access;
- local cache is disposable acceleration, not the authoritative filesystem copy.

For object storage, confirm bucket and prefix ownership, endpoint, region, credentials, lifecycle rules, capacity, and availability. Do not assume an empty prefix or exclusive ownership without evidence.

## Create and mount deliberately

Use an exact released JuiceFS client and record:

- client version and installation source;
- redacted metadata URL and metadata engine type;
- object storage type, endpoint, bucket, and prefix;
- filesystem name and UUID after creation;
- mount, cache, metrics, and process-management options;
- service ownership and operating-system user.

Before running `juicefs format`, verify that the metadata target is intentionally new and that the bucket or prefix is correct. Formatting is not a connectivity probe, and it must not be repeated against an existing filesystem merely to test access.

Before mounting, verify filesystem identity and configuration. Explain the operational implications of options that affect writeback, cache placement, trash, metadata backup, permissions, or resource usage.

## Prepare for production

Require a documented owner and acceptance criteria for:

- metadata high availability, backups, restore tests, and capacity;
- object storage availability, lifecycle policy, credentials, and billing or quotas;
- client metrics, alerting, log retention, time synchronization, and upgrades;
- cache paths, capacity limits, eviction behavior, and failure handling;
- UID, GID, mode, ACL, quota, and actual workload compatibility.

Validate the real workload path. A successful mount alone does not prove data-path durability, cross-client visibility, permissions, or recovery readiness.

## Operate with read-only evidence first

Collect current versions, topology, filesystem identity, health, metrics, and logs before changing state.

Metadata import or restore, configuration repair, leaked-object deletion, destructive garbage collection, compaction, filesystem destruction, and object lifecycle changes require version-matched documentation, a verified backup, explicit scope, approval, and rollback or recovery steps.

For upgrades, read release notes for every skipped version and verify metadata, mixed-client, gateway, and CSI compatibility. Do not assume an in-place binary replacement is sufficient for every topology.

## Acceptance checklist

Confirm at least:

1. Expected clients can mount or connect after restart.
2. A unique test file survives close, reopen, readback, and checksum verification.
3. Cross-client visibility and permissions behave as designed.
4. Metrics and logs identify the filesystem and client version.
5. Metadata backup and restore procedures are documented and tested on a controlled target.
6. Owners know the rollback and escalation path.
