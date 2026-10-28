# Contributing to JuiceFS

## Guidelines

- Before starting work, search existing issues and PRs for related reports and ongoing work.
- Discuss new features and changes to public interfaces or persisted formats in an issue before implementing them. For a major change, explain the design and tradeoffs so we can agree on the direction first.
- Small, self-contained fixes and documentation corrections can explain their motivation directly in the PR without opening a separate issue.
- A good way to find a project properly sized for a first time contributor is to search for open issues with the label ["kind/good-first-issue"](https://github.com/juicedata/juicefs/labels/kind%2Fgood-first-issue) or ["kind/help-wanted"](https://github.com/juicedata/juicefs/labels/kind%2Fhelp-wanted).

## Reporting Issues

- Use [Discussions](https://github.com/juicedata/juicefs/discussions) or [Discord](https://go.juicefs.com/discord) for setup and usage questions. Use the bug or enhancement template for problems and proposals.
- For bugs, describe what happened, what you expected, the affected version and relevant environment. Provide minimal reproduction steps when possible. For intermittent or production-only failures, logs or a specific code path are useful even without a reliable reproduction; you do not need to establish the root cause before reporting.
- Separate observed results from suspected causes and suggested reproduction steps you have not run. State which versions and environments you actually checked.
- Keep logs relevant and remove credentials and private data. Clearly describe any suspected data loss or corruption so maintainers can prioritize investigation.
- For enhancements, describe the workload, the limitation it encounters, and why existing options are insufficient. A proposed implementation is optional.

## AI-Assisted Contributions

AI-assisted contributions follow the same quality standards as other contributions. You are responsible for reading, understanding and checking everything you submit, and for answering questions during review.

- If AI generated substantial code, analysis or report text, briefly describe how it was used. Translation or copy-editing alone does not require disclosure; full prompts or chat transcripts are not needed.
- Verify generated claims and references. Do not present unverified tool output as an established fact or claim checks you did not run.
- Reviewers should assess evidence and the change itself, not infer quality from writing style or tool use.

## Coding Style

- We're following ["Effective Go"](https://go.dev/doc/effective_go) and ["Go Code Review Comments"](https://github.com/golang/go/wiki/CodeReviewComments).
- Use `go fmt` to format your code before committing. You can find information in editor support for Go tools in ["IDEs and Plugins for Go"](https://github.com/golang/go/wiki/IDEsAndTextEditorPlugins).
- Keep style cleanups separate from unrelated fixes or features, and explain their benefit. Add comments where the reasoning is not evident from the code; do not add comments just to satisfy a checklist.
- Every new source file must begin with a license header.
- Install [pre-commit](https://pre-commit.com) and use it to set up a pre-commit hook for static analysis. Just run `pre-commit install` in the root of the repo.

## Sign the CLA

Before you can contribute to JuiceFS, you will need to sign the [Contributor License Agreement](https://cla-assistant.io/juicedata/juicefs). There're a CLA assistant to guide you when you first time submit a pull request.

## What Makes a PR Ready for Review

- Explain the problem or use case, why the change addresses it, and any known limitations or open design questions. Link related issues and PRs instead of repeating their full history. Keep small changes brief.
- Keep each PR focused on one purpose. Do not mix in unrelated refactoring, formatting changes or features.
- Cover behavior changes with focused regression tests, reusing existing coverage where it already verifies the behavior. Record the commands and results, and identify checks you could not run. Documentation-only changes do not need code tests.
- Explain compatibility and failure-recovery implications when relevant. In particular, metadata changes need consideration of Redis, SQL and KV behavior, persisted formats, dump/load and mixed-version clients; follow the repository constraints in [AGENTS.md](AGENTS.md).
- Update the relevant English and Chinese documentation for user-facing behavior changes.
- Review your own diff, follow the coding style, and use explanatory commit messages.

Maintainers may ask for missing evidence, a narrower scope or a design discussion before reviewing the implementation. A successful CI run supports the review but does not establish that the approach is correct.

## Contribution Flow

This is a rough outline of what a contributor's workflow looks like:

1. Create a topic branch from where to base the contribution. This is usually `main`.
1. Make commits of logical units.
1. Make sure commit messages are in the proper format.
1. Push changes in a topic branch to a personal fork of the repository.
1. Submit a pull request to [juicedata/juicefs](https://github.com/juicedata/juicefs/compare) using the PR template. Link the issue discussed above, or explain a small, self-contained change directly.
1. The PR must receive approval from at least one maintainer before it be merged.

Happy hacking!
