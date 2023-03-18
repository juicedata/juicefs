---
title: Contributing Guide
sidebar_position: 1
description: JuiceFS is open source software and the code is contributed and maintained by developers worldwide. Learn how to participate in this article.
---

## Learn source code

Assuming you're already familiar with Go, as well as [JuiceFS architecture](https://juicefs.com/docs/community/architecture), this is the overall code structure:

* [`cmd`](https://github.com/juicedata/juicefs/tree/main/cmd) is the top-level entrance, all JuiceFS functionalities is rooted here, e.g. the `juicefs format` command resides in `cmd/format.go`；
* [`pkg`](https://github.com/juicedata/juicefs/tree/main/pkg) is actual implementation:
  * `pkg/fuse/fuse.go` provides abstract FUSE API;
  * `pkg/vfs` contains actual FUSE implementation, Metadata requests are handled in `pkg/meta`, read requests are handled in `pkg/vfs/reader.go` and write requests are handled by `pkg/vfs/writer.go`;
  * `pkg/meta` directory is the implementation of all metadata engines, where:
    * `pkg/meta/interface.go` is the interface definition for all types of metadata engines
    * `pkg/meta/redis.go` is the interface implementation of Redis database
    * `pkg/meta/sql.go` is the interface definition and general interface implementation of relational database, and the implementation of specific databases is in a separate file (for example, the implementation of MySQL is in `pkg/meta/sql_mysql.go`)
    * `pkg/meta/tkv.go` is the interface definition and general interface implementation of the KV database, and the implementation of a specific database is in a separate file (for example, the implementation of TiKV is in `pkg/meta/tkv_tikv.go`)
  * `pkg/object` contains all object storage integration code;
* [`sdk/java`](https://github.com/juicedata/juicefs/tree/main/sdk/java) is the Hadoop Java SDK, it uses `sdk/java/libjfs` through JNI.

The read and write request processing flow of JuiceFS can be read [here](../introduction/io_processing.md), and the key data structure can be read ["Internals"](./data_structures.md).

## Guidelines

- Before starting work on a feature or bug fix, search GitHub or reach out to us via GitHub or Slack, make sure no one else is already working on it and we'll ask you to open a GitHub issue if necessary.
- Before contributing, use the GitHub issue to discuss the feature and reach an agreement with the core developers.
- For major feature updates, write a design document to help the community understand your motivation and solution.
- Find issues with the label ["kind/good-first-issue"](https://github.com/juicedata/juicefs/labels/kind%2Fgood-first-issue) or ["kind/help-wanted"](https://github.com/juicedata/juicefs/labels/kind%2Fhelp-wanted).

Read [internals](./data_structures.md) for important data structure references.

## Coding style

- We're following ["Effective Go"](https://go.dev/doc/effective_go) and ["Go Code Review Comments"](https://github.com/golang/go/wiki/CodeReviewComments).
- Use `go fmt` to format your code before committing. You can find information in editor support for Go tools in ["IDEs and Plugins for Go"](https://github.com/golang/go/wiki/IDEsAndTextEditorPlugins).
- Every new source file must begin with a license header.
- Install [pre-commit](https://pre-commit.com) and use it to set up a pre-commit hook for static analysis. Just run `pre-commit install` in the root of the repo.

## Sign the CLA

Before you can contribute to JuiceFS, you will need to sign the [Contributor License Agreement](https://cla-assistant.io/juicedata/juicefs). There're a CLA assistant to guide you when you first time submit a pull request.

## What is a good PR

- Presence of unit tests
- Adherence to the coding style
- Adequate in-line comments
- Explanatory commit message

## Contribution flow

1. Create a topic branch from where to base the contribution. This is usually `main`.
1. Make commits of logical units.
1. Make sure commit messages are in the proper format.
1. Push changes in a topic branch to a personal fork of the repository.
1. Submit a pull request to [`juicedata/juicefs`](https://github.com/juicedata/juicefs/compare). The PR should link to one issue which either created by you or others.
1. The PR must receive approval from at least one maintainer before it be merged.
