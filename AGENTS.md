# AGENTS.md

JuiceFS is a POSIX-compatible distributed file system written in Go
(`module github.com/juicedata/juicefs`). A client coordinates a **metadata engine**
and **object storage**, exposing POSIX (FUSE) and an S3 gateway, plus Java/Hadoop
(`sdk/java/`) and Python (`sdk/python/`) SDKs.

Metadata engine families under `pkg/meta/`:

- **Redis** — `redisMeta` (`redis.go`); also KeyDB.
- **SQL/DB** — `dbMeta` (`sql.go`): MySQL, PostgreSQL, SQLite.
- **KV (TKV)** — `kvMeta` (`tkv.go`): TiKV, etcd, BadgerDB, FoundationDB.

## Repository map

Entry points: `main.go` (root) and `cmd/main.go` (CLI commands live in `cmd/`).

| Path           | Responsibility                                                  |
| -------------- | --------------------------------------------------------------- |
| `cmd/`         | CLI subcommands (`mount`, `gateway`, `sync`, `format`, `gc`, …) |
| `pkg/meta/`    | Metadata engine abstraction + per-engine implementations        |
| `pkg/vfs/`     | Virtual filesystem layer (POSIX semantics)                      |
| `pkg/fuse/`    | FUSE bindings (Linux/macOS); `pkg/winfsp/` for Windows          |
| `pkg/fs/`      | High-level filesystem logic                                     |
| `pkg/chunk/`   | Chunk / slice / block data management and caching               |
| `pkg/object/`  | Object storage backend abstraction                              |
| `pkg/gateway/` | S3-compatible gateway                                           |
| `pkg/sync/`    | Data synchronization (`juicefs sync`)                           |
| `pkg/acl/`     | POSIX ACL support                                               |
| `docs/`        | Documentation: `docs/en/` (English), `docs/zh_cn/` (Chinese)    |

## Build

```sh
make juicefs                 # standard build -> ./juicefs
STATIC=1 make juicefs        # static binary (needs musl-gcc)
make BUILD=debug all         # debug build (-N -l)
make juicefs.lite            # minimal build, most backends disabled
make juicefs.ceph            # -tags ceph
make juicefs.fdb             # -tags fdb (FoundationDB)
```

Local volume for manual testing (SQLite metadata):

```sh
./juicefs format sqlite3://test.db myjfs    # create a volume
./juicefs mount  sqlite3://test.db /tmp/jfs # mount it
```

## Test

Use the smallest target covering your change. Targets are in the `Makefile`
and mirror CI (`.github/workflows/unittests.yml`).

```sh
make test.meta.core          # ./pkg/meta/... core (no external services)
make test.meta.non-core      # Redis/PostgreSQL/etcd/KeyDB engine tests
make test.pkg                # all ./pkg/... except meta (-tags gluster)
make test.cmd                # ./cmd/... (needs MinIO env, runs under sudo)
make test.fdb                # FoundationDB tests (-tags fdb)
```

| Change scope       | Run                                                               |
| ------------------ | ----------------------------------------------------------------- |
| `pkg/meta/**`      | `make test.meta.core` (+ `test.meta.non-core` if engine-specific) |
| `cmd/**`           | `make test.cmd`                                                   |
| any other `pkg/**` | `make test.pkg`                                                   |

- When fixing a bug, add a regression test that fails before the fix and passes after.
- Group new test cases for the same module/category together; extend an existing test
  rather than scattering new cases.

## Lint & format

- Run `go fmt` before committing.
- Linting uses `golangci-lint` per `.golangci.yml`; pre-commit pins v1.52.2 and CI runs v2.6 (see `.github/workflows/verify.yml`).
- Install hooks once with `pre-commit install` (config in `.pre-commit-config.yaml`).

## Code style & license header

- Follow [Effective Go](https://go.dev/doc/effective_go) and
  [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments).
- Keep comments minimal; add only when necessary.
- Every new `.go` file MUST start with the Apache 2.0 header (see `main.go` for the canonical template).

## Version compatibility

- Persistent metadata or serialization changes in `pkg/meta/{interface,redis,sql,tkv}.go`
  must remain readable by new clients, and old clients must not silently drop new
  fields when rewriting records.
- When metadata fields change, review `pkg/meta/{dump,backup}.go`, `pkg/meta/*_bak.go`,
  and `pb/backup.proto`. Released dump/load formats must remain readable; tolerate
  unknown fields where feasible, reject unsupported formats explicitly, and never
  silently lose correctness-critical data.
- Evaluate mixed-version behavior for metadata features or semantic changes. If
  unsafe, raise (never lower) `MinClientVersion` and enable the feature only after
  old clients have exited.
- FUSE option changes must preserve existing names and defaults. Review graceful
  restart, `FuseOptions`, `StripOptions`, and old-config normalization in
  `cmd/mount_unix.go`, `pkg/vfs/vfs.go`, and `pkg/fuse/fuse.go`.
- Add compatibility tests, or explicitly report missing coverage during review.

## Agent boundaries

- Correctness first: this is a distributed file system; small changes can affect data
  integrity. Do not invent APIs, defaults, or behavior — verify against the code, and
  don't bypass safety checks.
- Metadata-engine parity: a semantic change in `pkg/meta/` must behave identically
  across all three families (Redis, SQL/DB, KV) and be covered by their shared tests.
- Behavior changes need matching unit tests; user-facing changes update the docs.
- Keep diffs minimal and scoped; avoid unrelated refactors or formatting-only churn.
- Do not hand-edit generated code or vendored dependencies.
- Match existing conventions in the file you are editing.
- Confirm before destructive or hard-to-reverse actions (deleting files, force pushes,
  schema/data changes).
