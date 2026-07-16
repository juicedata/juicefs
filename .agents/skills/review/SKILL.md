---
name: review
description: "Use when: performing code review, PR review, issue-fix review, patch review, implementation review, risk analysis, test coverage review, or verifying whether a reported bug/requirement is real before judging a change."
argument-hint: "[PR | diff | commit | issue | files]"
user-invocable: true
---

# Review

Use this skill to perform evidence-based reviews of code changes, pull requests, issue fixes, design changes, or implementation plans. Do not assume that the user's description, issue report, or PR summary is correct; verify the background first.

## When to Use

- Reviewing a PR, commit, diff, patch, code snippet, or specified files.
- Checking whether a reported bug, requirement, or risk actually exists.
- Evaluating whether a fix is correct, complete, compatible, and maintainable.
- Assessing implementation quality, performance impact, concurrency/data-consistency risks, test coverage, and regression risk.

## Core Principles

1. **Verify the background before judging the change**: identify the concrete problem, trigger conditions, affected behavior, and whether the problem is real.
2. **Prefer primary evidence**: base important conclusions on code paths, tests, command output, official documentation, specifications, or first-party project documentation.
3. **Report only actionable issues**: avoid style-only, preference-based, or low-value comments unless they hide a correctness, maintainability, or risk issue.
4. **Separate facts from inferences**: label uncertainty clearly; do not present guesses as confirmed findings.
5. **Respect project context**: follow existing architecture, naming, error handling, logging, tests, and repository instructions.

## Procedure

### 1. Identify the Review Scope

- Confirm whether the target is a PR, branch diff, commit, issue, file list, or code snippet.
- If the scope is unclear, ask for clarification instead of expanding to unrelated files.
- For GitHub PRs or issues, prefer `gh` to inspect the title, description, comments, linked issues, and diff.

### 2. Verify Background and Problem Reality

Answer these questions before evaluating the implementation:

- What exact problem is claimed? What are the trigger conditions, affected users, and visible behavior?
- Does the problem actually exist? Can it be verified from code paths, tests, logs, existing behavior, history, or official documentation?
- Is the description inaccurate, incomplete, missing reproduction details, or confusing expected behavior with a bug?
- If behavior depends on a language, framework, protocol, operating system, storage backend, or external service, check official documentation or first-party sources and cite the relevant evidence.

If the background cannot be verified, state what is unverified and what evidence is missing.

### 3. Review the Proposed Fix or Design

Check whether the approach:

- Addresses the root cause rather than only masking a symptom.
- Covers main paths, boundary conditions, error paths, compatibility, migration, rollback, and failure modes.
- Fits existing abstractions and does not bypass helpers, validation, locking, transactions, permission checks, or safety mechanisms.
- Avoids unnecessary behavior changes and keeps user-facing semantics compatible unless the change is intentional and documented.
- For JuiceFS changes, pay special attention to POSIX semantics, data integrity, metadata-engine parity, dump/load compatibility, object-storage semantics, concurrency, and failure recovery.

### 4. Review Code Implementation

Evaluate the implementation across these dimensions:

- **Correctness**: logic matches the real trigger path; no new race conditions, nil dereferences, bounds issues, resource leaks, transaction inconsistencies, or error propagation bugs.
- **Simplicity**: the code is direct and readable; no unnecessary abstraction, duplication, over-engineering, or hidden side effects.
- **Project consistency**: follows existing patterns for naming, helpers, error handling, logging, configuration, tests, and Go idioms.
- **Performance**: avoids avoidable I/O, lock contention, full scans, repeated network calls, memory amplification, or hot-path overhead.
- **Security and reliability**: preserves permissions, path handling, input validation, authentication, sensitive-data handling, data integrity, and recoverability.
- **Compatibility**: does not break existing APIs, CLIs, configuration, serialized formats, cross-version data, or behavior across metadata/object-storage backends.

### 5. Review Test Coverage

Check whether tests are appropriate and identify gaps:

- Do tests reproduce the original problem and verify the fixed behavior?
- Do they cover boundary cases, error paths, concurrency, compatibility, and backend-specific behavior when relevant?
- Are tests stable, minimal, readable, and consistent with existing helpers and test organization?
- Are the suggested or executed test commands appropriate for the change scope?
- If tests were not run, explain why and recommend the smallest relevant commands.

### 6. Output Format

Use this structure for review results:

```markdown
## Review Summary
- Verdict: Approve / Request changes / Comment only / Need more information
- Main risks: ...
- Evidence checked: ...

## Background Verification
- Claimed problem: ...
- Reality check: confirmed / partially confirmed / not confirmed / unable to verify
- Evidence: code locations, official docs, tests, or command output
- Unknowns: ...

## Findings
### [Severity] Title
- Location: `path:line`
- Problem: ...
- Impact: ...
- Evidence: ...
- Recommendation: ...
```

If there are no blocking findings, explicitly state which dimensions were checked and which items remain unverified.

## Severity Rubric

- **Critical**: likely data loss/corruption, permission bypass, security vulnerability, severe outage, or unrecoverable incompatibility.
- **High**: likely real-world wrong behavior, crash, major performance regression, or key feature regression.
- **Medium**: edge-case defect, compatibility risk, or test gap that may hide a real regression.
- **Low**: minor risk or maintainability improvement; do not block changes for pure style preference.

## Review Discipline

- Do not skip background verification just because the diff looks reasonable.
- Do not conclude solely from the PR or issue description.
- Do not leave vague comments without concrete impact and actionable recommendations.
- Do not request unrelated refactors or formatting-only changes.
- Default to review-only behavior; do not modify code unless the user explicitly asks for fixes.
