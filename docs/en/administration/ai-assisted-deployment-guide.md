---
title: JuiceFS AI Deployment Assistant User Guide
sidebar_position: 10
description: Install and use the JuiceFS Community Support Skill for Community Edition deployment, operations, troubleshooting, recovery, and Kubernetes integration.
---

The JuiceFS AI-Assisted Deployment Assistant is an Agent Skill for JuiceFS Community Edition. It combines version-matched Community documentation and public source code with sanitized evidence from the target environment. It can help plan, validate, operate, and troubleshoot clients, metadata engines, object storage, FUSE mounts, gateways, cache, upgrades, recovery, and Community CSI integration.

Install the complete skill directory once. During a task, the assistant loads only the reference files relevant to the selected scenario.

## Prerequisites {#prerequisites}

Before installation:

- use an AI coding agent that can load a directory containing `SKILL.md` and supporting reference files;
- identify the deployed JuiceFS client, CSI Driver, client image, and Kubernetes versions relevant to the task;
- identify the metadata engine, data storage backend, access path, and existing file system state that must be preserved;
- ensure that the agent can access the public documentation and repositories, or provide local documentation and source roots with their release tags, commits, or download dates;
- arrange separate authorization for every state-changing operation.

Do not provide complete metadata URLs with passwords, object-storage keys, encryption material, kubeconfigs, or Kubernetes Secret values. Share only focused and sanitized evidence.

## Install the complete skill {#install}

Download the skill to a temporary review directory:

```shell
SKILL_BASE=https://raw.githubusercontent.com/juicedata/juicefs/main/.agents/skills/juicefs-community-support
REVIEW_DIR=$(mktemp -d)
mkdir -p "$REVIEW_DIR/references"

curl -fsSL "$SKILL_BASE/SKILL.md" -o "$REVIEW_DIR/SKILL.md"
for file in community-deployment.md community-csi.md source-code.md troubleshooting.md; do
  curl -fsSL "$SKILL_BASE/references/$file" -o "$REVIEW_DIR/references/$file"
done

find "$REVIEW_DIR" -type f -print
less "$REVIEW_DIR/SKILL.md"
```

Choose an installation target:

| Agent runtime | Project scope | User scope |
| --- | --- | --- |
| Claude Code | `.claude/skills/juicefs-community-support` | `~/.claude/skills/juicefs-community-support` |
| Codex | `.agents/skills/juicefs-community-support` | `~/.agents/skills/juicefs-community-support` |
| Cursor | `.cursor/skills/juicefs-community-support` or `.agents/skills/juicefs-community-support` | `~/.cursor/skills/juicefs-community-support` or `~/.agents/skills/juicefs-community-support` |
| ZCode | Use user scope or import an external Agent Skill from **Settings > Skills** | `~/.zcode/skills/juicefs-community-support` |
| DeepSeek Harness | `.dsh/skills/juicefs-community-support` or `.agents/skills/juicefs-community-support` | `~/.dsh/skills/juicefs-community-support` or `~/.agents/skills/juicefs-community-support` |
| Qwen Code (Alibaba Cloud) | `.qwen/skills/juicefs-community-support` | `~/.qwen/skills/juicefs-community-support` |
| CodeBuddy (Tencent Cloud) | `.codebuddy/skills/juicefs-community-support` | `~/.codebuddy/skills/juicefs-community-support` |
| TRAE (ByteDance) | `.trae/skills/juicefs-community-support` | `~/.trae/skills/juicefs-community-support` |

Copy the complete reviewed directory. This example uses Codex project scope:

```shell
SKILL_DIR=.agents/skills/juicefs-community-support
mkdir -p "$SKILL_DIR/references"
install -m 0644 "$REVIEW_DIR/SKILL.md" "$SKILL_DIR/SKILL.md"
install -m 0644 "$REVIEW_DIR"/references/*.md "$SKILL_DIR/references/"
```

Do not translate or rename `SKILL.md` or its reference files. Refresh the runtime Skills page or start a new task after installation. A runtime that imports only `SKILL.md` receives the core safeguards but not complete scenario coverage.

## Choose a Community Edition scenario {#use}

Example requests:

- “Help me prepare a production JuiceFS Community Edition deployment with PostgreSQL metadata and S3-compatible object storage.”
- “Review this existing file system before I upgrade all clients.”
- “The mount reports an I/O error. Diagnose the first failing layer without changing anything.”
- “Investigate this behavior against the source code for the deployed release.”
- “Help me troubleshoot a Community Edition volume through JuiceFS CSI Driver.”

| Scenario | Reference files loaded |
| --- | --- |
| Deployment, production readiness, maintenance, or upgrade | `community-deployment.md` |
| Kubernetes, PVC, Mount Pod, sidecar, or Operator | `community-csi.md` plus the relevant Community reference |
| Version-sensitive or undocumented behavior | `source-code.md` plus the relevant scenario reference |
| Focused failure, suspected data loss, or recovery | `troubleshooting.md` plus the relevant component reference |

## Coverage and limits {#coverage}

The assistant covers Community Edition clients, metadata engines, data storage backends, FUSE and gateway access, local cache, monitoring, upgrades, backup and recovery, public source investigation, and Community CSI integration.

It does not act as an unattended installer. It does not treat an unpinned development branch as proof of deployed behavior, recommend unreleased production builds by default, invent flags or configuration fields, or perform changes without target-specific approval.

## Safety and acceptance {#safety}

Before formatting, importing or repairing metadata, deleting leaked objects, running destructive garbage collection, deleting Kubernetes resources, changing a bucket or prefix, rotating credentials, or destroying a filesystem, the assistant must identify the exact target, verify backup or recovery material, state the expected effect and rollback, and obtain explicit approval.

A successful mount, Bound PVC, or Running Mount Pod is not sufficient proof. Acceptance requires a scoped workload to create a unique file, close it, reopen it, read it back, verify its checksum, and confirm cross-client or cross-Pod visibility when the topology requires it.

## Review the skill contents {#skill-content}

The entrypoint contains the always-on routing and safety boundaries. Detailed procedures are loaded only when the selected scenario requires them.

| File | Purpose |
| --- | --- |
| [`SKILL.md`](https://github.com/juicedata/juicefs/blob/main/.agents/skills/juicefs-community-support/SKILL.md) | Scope, evidence order, source rules, safety boundaries, and acceptance |
| [`community-deployment.md`](https://github.com/juicedata/juicefs/blob/main/.agents/skills/juicefs-community-support/references/community-deployment.md) | Deployment, topology, production readiness, operations, and upgrades |
| [`community-csi.md`](https://github.com/juicedata/juicefs/blob/main/.agents/skills/juicefs-community-support/references/community-csi.md) | Community CSI provisioning, mount lifecycle, evidence, and acceptance |
| [`source-code.md`](https://github.com/juicedata/juicefs/blob/main/.agents/skills/juicefs-community-support/references/source-code.md) | Version-matched JuiceFS and CSI Driver source investigation |
| [`troubleshooting.md`](https://github.com/juicedata/juicefs/blob/main/.agents/skills/juicefs-community-support/references/troubleshooting.md) | Focused diagnosis, evidence preservation, remediation, and recovery |

## Complete SKILL.md {#complete-skill}

````markdown
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
````

## Update or remove the skill {#update}

To update, download all files to a new temporary directory, review the changes, and replace the complete installed directory. Do not update only `SKILL.md` because its routing may depend on updated references.

To remove the skill, delete only the installed `juicefs-community-support` directory from the project or user scope that you selected. This does not change any JuiceFS filesystem, client, metadata engine, or Kubernetes resource.
