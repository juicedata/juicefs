---
title: JuiceFS AI 辅助部署助手使用指南
sidebar_position: 10
description: 安装并使用 JuiceFS 社区版 Support Skill，辅助社区版部署、运维、排障、恢复和 Kubernetes 接入。
---

JuiceFS AI 辅助部署助手是一项面向 JuiceFS 社区版的 Agent Skill。它结合与实际版本匹配的社区版文档、公开源代码和目标环境中的脱敏证据，辅助规划、验证、运维和排查客户端、元数据引擎、数据存储后端、FUSE 挂载、网关、缓存、升级、恢复及社区版 CSI 接入问题。

只需安装一次完整 Skill 目录。执行任务时，助手仅加载当前场景需要的 reference 文件。

## 前置条件 {#prerequisites}

安装前请确认：

- AI 编程 Agent 能加载包含 `SKILL.md` 和 reference 文件的完整目录；
- 已确认任务相关的 JuiceFS 客户端、CSI Driver、客户端镜像及 Kubernetes 版本；
- 已确认元数据引擎、数据存储后端、访问方式，以及必须保留的现有文件系统状态；
- Agent 可以访问公开文档和仓库；如果处于离线环境，则提供本地文档及源码根目录，并说明其 release tag、commit 或下载日期；
- 每个状态变更都由环境责任人单独授权。

不要提供包含密码的完整元数据 URL、对象存储密钥、加密材料、kubeconfig 或 Kubernetes Secret 值。只提供聚焦且脱敏的证据。

## 安装完整 Skill {#install}

先将 Skill 下载到临时审阅目录：

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

选择安装位置：

| Agent 运行时 | 项目范围 | 用户范围 |
| --- | --- | --- |
| Claude Code | `.claude/skills/juicefs-community-support` | `~/.claude/skills/juicefs-community-support` |
| Codex | `.agents/skills/juicefs-community-support` | `~/.agents/skills/juicefs-community-support` |
| Cursor | `.cursor/skills/juicefs-community-support` 或 `.agents/skills/juicefs-community-support` | `~/.cursor/skills/juicefs-community-support` 或 `~/.agents/skills/juicefs-community-support` |
| ZCode | 使用用户范围，或在 **Settings > Skills** 中导入外部 Agent Skill | `~/.zcode/skills/juicefs-community-support` |
| DeepSeek Harness | `.dsh/skills/juicefs-community-support` 或 `.agents/skills/juicefs-community-support` | `~/.dsh/skills/juicefs-community-support` 或 `~/.agents/skills/juicefs-community-support` |
| Qwen Code（阿里云） | `.qwen/skills/juicefs-community-support` | `~/.qwen/skills/juicefs-community-support` |
| CodeBuddy（腾讯云） | `.codebuddy/skills/juicefs-community-support` | `~/.codebuddy/skills/juicefs-community-support` |
| TRAE（字节跳动） | `.trae/skills/juicefs-community-support` | `~/.trae/skills/juicefs-community-support` |

复制审阅后的完整目录。以下示例使用 Codex 项目范围：

```shell
SKILL_DIR=.agents/skills/juicefs-community-support
mkdir -p "$SKILL_DIR/references"
install -m 0644 "$REVIEW_DIR/SKILL.md" "$SKILL_DIR/SKILL.md"
install -m 0644 "$REVIEW_DIR"/references/*.md "$SKILL_DIR/references/"
```

请勿翻译或重命名 `SKILL.md` 及其 reference 文件。安装后刷新运行时的 Skills 页面，或者新建任务。只导入 `SKILL.md` 可以获得核心安全规则，但无法获得完整场景覆盖。

## 选择社区版场景 {#use}

示例请求：

- 「帮助我准备使用 PostgreSQL 元数据和 S3 兼容对象存储的 JuiceFS 社区版生产部署。」
- 「升级全部客户端之前，帮我审阅现有文件系统。」
- 「挂载点报告 I/O error，请从第一个失败层开始排查，不要修改环境。」
- 「根据已部署版本的源代码核对这个行为。」
- 「帮助我排查通过 JuiceFS CSI Driver 使用社区版卷的问题。」

| 场景 | 加载的 reference 文件 |
| --- | --- |
| 部署、生产就绪、维护或升级 | `community-deployment.md` |
| Kubernetes、PVC、Mount Pod、Sidecar 或 Operator | `community-csi.md` 及相关社区版 reference |
| 版本敏感或文档未明确的行为 | `source-code.md` 及相关场景 reference |
| 聚焦故障、疑似数据丢失或恢复 | `troubleshooting.md` 及相关组件 reference |

## 覆盖范围与限制 {#coverage}

助手覆盖社区版客户端、元数据引擎、数据存储后端、FUSE 和网关访问、本地缓存、监控、升级、备份恢复、公开源码核对及社区版 CSI 接入。

它不是无人值守安装器；不会把未固定版本的开发分支当作已部署行为的证据，不会默认建议使用未发布源码构建生产版本，不会编造参数或配置字段，也不会在缺少明确目标授权时执行变更。

## 安全与验收 {#safety}

执行格式化、元数据导入或修复、泄漏对象删除、破坏性垃圾回收、Kubernetes 资源删除、Bucket 或 Prefix 变更、凭据轮换或文件系统销毁前，助手必须确认确切目标，核对备份或恢复材料，说明预期影响和回滚方式，并获得明确授权。

成功挂载、Bound PVC 或 Running Mount Pod 都不足以证明部署完成。验收必须由限定范围的工作负载创建唯一文件，关闭并重新打开，读回并校验 checksum；拓扑需要时，还要确认跨客户端或跨 Pod 可见性。

## 审阅 Skill 内容 {#skill-content}

入口文件保存始终生效的路由和安全边界。只有当前场景需要时，助手才加载详细 reference。

| 文件 | 用途 |
| --- | --- |
| [`SKILL.md`](https://github.com/juicedata/juicefs/blob/main/.agents/skills/juicefs-community-support/SKILL.md) | 适用范围、证据顺序、源码规则、安全边界和验收 |
| [`community-deployment.md`](https://github.com/juicedata/juicefs/blob/main/.agents/skills/juicefs-community-support/references/community-deployment.md) | 部署、拓扑、生产就绪、运维和升级 |
| [`community-csi.md`](https://github.com/juicedata/juicefs/blob/main/.agents/skills/juicefs-community-support/references/community-csi.md) | 社区版 CSI provisioning、挂载生命周期、证据和验收 |
| [`source-code.md`](https://github.com/juicedata/juicefs/blob/main/.agents/skills/juicefs-community-support/references/source-code.md) | 按版本核对 JuiceFS 与 CSI Driver 公开源码 |
| [`troubleshooting.md`](https://github.com/juicedata/juicefs/blob/main/.agents/skills/juicefs-community-support/references/troubleshooting.md) | 聚焦诊断、证据保全、修复和恢复 |

## 完整 SKILL.md {#complete-skill}

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

## 更新或移除 Skill {#update}

更新时，请把全部文件下载到新的临时目录，审阅变更后替换完整安装目录。不要只更新 `SKILL.md`，因为它的路由可能依赖已经更新的 reference。

移除时，只删除所选项目范围或用户范围内的 `juicefs-community-support` 安装目录。该操作不会修改任何 JuiceFS 文件系统、客户端、元数据引擎或 Kubernetes 资源。
