# Contributing to JuiceFS

## Guidelines

- Before starting work on a feature or bug fix, please search GitHub or reach out to us via GitHub, Slack etc. The purpose of this step is make sure no one else is already working on it and we'll ask you to open a GitHub issue if necessary.
- We will use the GitHub issue to discuss the feature and come to agreement. This is to prevent your time being wasted, as well as ours.
- If it is a major feature update, we highly recommend you also write a design document to help the community understand your motivation and solution.
- A good way to find a project properly sized for a first time contributor is to search for open issues with the label ["kind/good-first-issue"](https://github.com/juicedata/juicefs/labels/kind%2Fgood-first-issue) or ["kind/help-wanted"](https://github.com/juicedata/juicefs/labels/kind%2Fhelp-wanted).

## Coding Style

- We're following ["Effective Go"](https://golang.org/doc/effective_go.html) and ["Go Code Review Comments"](https://github.com/golang/go/wiki/CodeReviewComments).
- Use `go fmt` to format your code before committing. You can find information in editor support for Go tools in ["IDEs and Plugins for Go"](https://github.com/golang/go/wiki/IDEsAndTextEditorPlugins).
- If you see any code which clearly violates the style guide, please fix it and send a pull request.
- Every new source file must begin with a license header.
- Install [pre-commit](https://pre-commit.com/) and use it to set up a pre-commit hook for static analysis. Just run `pre-commit install` in the root of the repo.

## Sign the CLA

Before you can contribute to JuiceFS, you will need to sign the [Contributor License Agreement](https://cla-assistant.io/juicedata/juicefs). There're a CLA assistant to guide you when you first time submit a pull request.

## What is a Good PR

- Presence of unit tests
- Adherence to the coding style
- Adequate in-line comments
- Explanatory commit message

## Contribution Flow

This is a rough outline of what a contributor's workflow looks like:

- Create a topic branch from where to base the contribution. This is usually `main`.
- Make commits of logical units.
- Make sure commit messages are in the proper format.
- Push changes in a topic branch to a personal fork of the repository.
- Submit a pull request to [juicedata/juicefs](https://github.com/juicedata/juicefs/compare). The PR should link to one issue which either created by you or others.
- The PR must receive approval from at least one maintainer before it be merged.

Happy hacking!
