# JuiceFS Go SDK examples

Each directory is a program that uses the Go SDK (`github.com/juicedata/juicefs/sdk/go`) to access a JuiceFS volume directly, without a FUSE mount.

| Example | What it does |
| --- | --- |
| [basic](basic/main.go) | Creates `/go-sdk-demo/basic`, writes, appends and reads a file, then renames it and lists the directory |
| [iofs](iofs/main.go) | Walks a directory with `fs.WalkDir` and matches files with `fs.Glob` through `Client.FS()` |
| [admin](admin/main.go) | Uses the interfaces specific to JuiceFS on `/go-sdk-demo/admin`: extended attributes, `Info`, `Summary`, quota, `Warmup`, `Status` and `SessionInfo` |
| [http](http/main.go) | Serves a directory read-only over HTTP with `http.FileServerFS(Client.FS())` until Ctrl-C |

## Prerequisites

- Go 1.25 or later, with cgo enabled (the SDK depends on cgo packages such as the SQLite driver and the compressors).
- Build with `-tags nogspt`. Without it, a dependency of JuiceFS clears the command line of the process shown by `ps`.
- A formatted volume. For a quick local test, format one on SQLite and a local directory:

  ```shell
  mkdir -p /tmp/jfs-demo
  go run -tags nogspt github.com/juicedata/juicefs format \
      --storage file --bucket /tmp/jfs-demo/data/ sqlite3:///tmp/jfs-demo/meta.db demo
  ```

## Run

The examples read the volume from two environment variables. Run them from the root of the repository:

```shell
export JFS_VOLUME=demo
export JFS_META_URL=sqlite3:///tmp/jfs-demo/meta.db

go run -tags nogspt ./sdk/go/examples/basic
go run -tags nogspt ./sdk/go/examples/admin
go run -tags nogspt ./sdk/go/examples/iofs go-sdk-demo
go run -tags nogspt ./sdk/go/examples/http -addr 127.0.0.1:8080 go-sdk-demo
```

- `JFS_VOLUME` is the name of the volume, used in metric labels and logs; `JFS_META_URL` is the URL of its metadata engine. Do not commit a URL with a password.
- The directory of `iofs` and `http` is in the form of `fs.ValidPath`: no leading `/`, and `.` is the root of the volume.
- `basic` and `admin` write into `/go-sdk-demo` and leave it for inspection, for example from a FUSE mount of the same volume. Remove it with `juicefs rmr` or `Client.RemoveAll` when done.
- `admin` sets and deletes a quota on `/go-sdk-demo/admin`. Quotas are managed as root without permission checks, as in the Python SDK and `juicefs quota`.
