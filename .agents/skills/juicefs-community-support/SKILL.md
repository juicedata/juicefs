---
name: juicefs-community-support
description: Support, deploy, operate, and troubleshoot JuiceFS Community Edition and its CSI integration. Use when a request involves open-source JuiceFS clients, metadata engines, object storage, FUSE mounts, gateways, cache, upgrades, recovery, Kubernetes PV or PVC behavior, Mount Pods, or implementation questions that may require reading the matching public source code.
---

# JuiceFS Community Support

## Outcome

Help users deploy, operate, and troubleshoot JuiceFS Community Edition and its Kubernetes integration using verified environment facts, version-matched public documentation and source code, sanitized runtime evidence, and explicit safety boundaries.

Do not assume JuiceFS Cloud Service or Enterprise Edition components, fields, workflows, or capabilities.

## Route the request

Before giving commands or conclusions, identify:

1. The stage: design, installation, deployment, validation, operation, upgrade, troubleshooting, or recovery.
2. Exact versions: JuiceFS client, CSI Driver, client image, Helm chart or manifests, and Kubernetes when relevant.
3. Topology: standalone or distributed, metadata engine, object storage, clients, and Kubernetes components.
4. Access path: FUSE, S3 Gateway, Hadoop SDK, CSI, sidecar, or another supported interface.
5. Existing state: new deployment, existing filesystem, degraded service, suspected data loss, or migration.

Load only the references needed for the request:

- Community deployment and operations: [deployment and operations reference](references/community-deployment.md)
- Community CSI integration: [Community CSI reference](references/community-csi.md)
- Version-matched source investigation: [source investigation reference](references/source-code.md)
- Troubleshooting and recovery: [troubleshooting and recovery reference](references/troubleshooting.md)

## Use evidence in this order

1. Public documentation that matches the deployed release.
2. Public source code at the matching release tag or exact commit.
3. Sanitized runtime evidence from the affected environment.
4. The current development branch, explicitly labeled as possible later behavior.
5. Inference, explicitly labeled and paired with a verification step.

If public documentation is unavailable, ask for the local JuiceFS source or documentation root and its release tag, commit, or download date. For the current Community repository layout, use `<JUICEFS_ROOT>/docs/en/` for English or `<JUICEFS_ROOT>/docs/zh_cn/` for Chinese. Read only the locale and topic relevant to the request, and do not assume that the local copy matches the deployed client version.

If documentation, source code, and runtime behavior disagree, report the mismatch. Do not silently choose one.

## Keep product and system layers separate

- Community Edition uses a customer-selected metadata engine and data storage backend. Object storage is common but not the only supported backend.
- Do not introduce Cloud Service console workflows, managed metadata services, Enterprise Edition distributed cache, license fields, or enterprise authentication fields.
- Separate the application, kernel cache, FUSE, VFS and buffers, local cache, metadata engine, object storage, gateway, CSI Controller, CSI Node, Mount Pod, sidecar, and workload Pod.
- Treat requests for other JuiceFS service models as outside this skill and use the documentation and operating model for the requested product.

## Read open-source code safely

Reading the public JuiceFS and CSI Driver source is allowed and encouraged when behavior is version-sensitive, undocumented, or unclear.

- Pin the deployed release tag, image version, or exact commit before drawing conclusions.
- Follow the real call path and cite repository, revision, file path, and relevant lines or symbols.
- Do not use an unpinned development branch as proof of behavior in an older deployed release.
- Do not recommend building unreleased source for production as the default remediation.
- Distinguish implementation facts from tests, commit intent, documentation, runtime observations, and inference.

See [references/source-code.md](references/source-code.md) for routing details.

## Protect credentials and data

Treat metadata URLs, database passwords, object storage keys, encryption material, kubeconfig content, and Kubernetes Secret values as sensitive. Ask for redacted evidence and never echo secrets in commands or reports.

A plan or diagnosis does not authorize changes. Before any state-changing action, state:

- exact target and scope;
- expected effect and risk;
- prerequisite backup or recovery point;
- verification and rollback procedure;
- approval required from the environment owner.

High-risk actions include filesystem initialization or replacement, metadata import or repair, leaked-object deletion, destructive garbage collection, cache removal, bucket or prefix changes, PV or PVC deletion, credential rotation, and changes to reclaim policy.

## Require end-to-end acceptance

For a controlled test path, create a unique file, close it, reopen it, read it back, and verify its checksum. When relevant, repeat from another client or Pod and correlate the result with events, logs, and metrics.

Do not equate any single intermediate state with success. Keep these separate:

- application write completion;
- `close` or `fsync` return;
- internal buffer flush;
- object upload;
- metadata commit;
- backend durability;
- a Bound PVC or Running Mount Pod;
- successful workload I/O.

## Communicate confidence

Label important findings as one of:

- **Verified:** observed directly in the target environment.
- **Documentation-based:** supported by version-matched public documentation.
- **Source-verified:** supported by version-matched public source code.
- **Unconfirmed:** evidence is insufficient or conflicting.
- **Inference:** a reasoned hypothesis with a concrete verification step.

Finish with the current state, unresolved risks, next safe action, and success criteria.

## Stop conditions

Stop and request clarification or explicit approval when:

- the target filesystem, cluster, bucket, prefix, metadata endpoint, PV, or PVC is ambiguous;
- credentials or recovery materials are missing;
- the available source revision does not match the deployed artifact;
- the next step could delete, overwrite, reinitialize, or irreversibly migrate data;
- suspected data loss or corruption requires preserving evidence before further action.
