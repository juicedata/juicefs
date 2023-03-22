---
title: Contributing Guide
sidebar_position: 1
description: JuiceFS is open source software and the code is contributed and maintained by developers worldwide. Learn how to participate in this article.
---

## Guidelines

- Before starting work on a feature or bug fix, search GitHub or reach out to us via GitHub or Slack, make sure no one else is already working on it and we'll ask you to open a GitHub issue if necessary.
- Before contributing, use the GitHub issue to discuss the feature and reach an agreement with the core developers.
- For major feature updates, write a design document to help the community understand your motivation and solution.
- Find issues with the label ["kind/good-first-issue"](https://github.com/juicedata/juicefs/labels/kind%2Fgood-first-issue) or ["kind/help-wanted"](https://github.com/juicedata/juicefs/labels/kind%2Fhelp-wanted).

Read [internals](./internals.md) for important data structure references.

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
