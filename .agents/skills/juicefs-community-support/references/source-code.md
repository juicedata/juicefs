# Open-Source Investigation

## Repositories and revisions

- JuiceFS Community Edition: [JuiceFS repository](https://github.com/juicedata/juicefs).
- JuiceFS CSI Driver: [JuiceFS CSI Driver repository](https://github.com/juicedata/juicefs-csi-driver).

Treat each repository's unpinned default branch as development code. Verify the current default branch rather than assuming its name.

Bind the investigation to the deployed binary or image:

1. Record the reported version, image digest, release tag, or build commit.
2. Resolve that identifier to an exact public revision.
3. Inspect files without changing the user's active worktree when possible, for example with `git show <REVISION>:<PATH>` and `git grep -n <PATTERN> <REVISION> -- <PATHS>`.
4. If only the development branch contains the relevant behavior, state the commit and date and label the finding as possible later behavior, not deployed behavior.

If a vendor-built artifact cannot be mapped to a public revision, report the evidence gap. Do not claim source verification.

## Community Edition source routing

Start from the command entry point and follow the call path:

- `main.go` and `cmd/main.go` for process and command registration;
- `cmd/<command>.go` for command flags, validation, and invocation;
- `pkg/meta/` for metadata clients, transactions, sessions, locks, quotas, and metadata maintenance;
- `pkg/object/` for object storage backends, requests, retries, and backend-specific behavior;
- `pkg/chunk/` for chunk I/O, upload, read, cache, and buffering paths;
- `pkg/vfs/` for filesystem semantics between FUSE or SDK entry points and lower layers;
- `pkg/fuse/` for FUSE request handling and kernel-facing behavior;
- `pkg/fs/` for higher-level filesystem operations;
- `pkg/gateway/` for S3 Gateway behavior;
- `pkg/sync/` for synchronization workflows.

Do not collapse application completion, FUSE response, VFS buffering, local cache admission, object upload, metadata commit, and backend durability into one event. Follow the actual path for the deployed revision.

## CSI Driver source routing

Trace the Kubernetes request through the driver and mount lifecycle:

- `cmd/` for component entry points and flags;
- `pkg/driver/controller.go` and `pkg/driver/provisioner.go` for controller services and provisioning;
- `pkg/driver/node.go` for node services, publish, unpublish, and cleanup;
- `pkg/juicefs/` for JuiceFS-specific orchestration;
- `pkg/juicefs/mount/` for mount creation, reuse, monitoring, and cleanup;
- `pkg/juicefs/mount/builder/` for client command and option construction;
- `pkg/config/` for configuration parsing, defaults, and precedence;
- `pkg/controller/` for Kubernetes reconciliation;
- `pkg/webhook/` for admission-time mutation;
- `pkg/util/resource/` for Kubernetes resource construction and lookup.

For a failure, identify the external entry point, validation and configuration path, client invocation, error wrapping, retry or timeout behavior, resource mutation, and cleanup path.

## Report source evidence precisely

For each source-backed conclusion, include:

- repository and exact revision;
- file path and relevant symbol or line range;
- the call chain that makes the code relevant;
- whether a condition is a default, optional branch, error path, or release-specific behavior;
- the runtime observation needed to prove that the path executed.

Classify claims as:

- **Source-verified:** proven by matching implementation code.
- **Observed:** proven by sanitized runtime evidence.
- **Documentation-based:** stated in version-matched public documentation.
- **Inference:** a hypothesis that still needs verification.

Tests and commit messages can clarify intent but do not replace the implementation path. Keep quotations small and prefer precise file and symbol citations.

Source inspection alone does not authorize compiling, deploying, or replacing production binaries. Treat any build or rollout as a separate reviewed change with compatibility, rollback, and acceptance criteria.
