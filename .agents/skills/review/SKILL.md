---
name: review
description: "Perform evidence-based reviews of PRs, commits, patches, files, issue fixes, designs, or implementation plans. Use for code review, risk and compatibility analysis, test coverage review, and verifying reported bugs or requirements before evaluating a change."
argument-hint: "[PR | diff | commit | issue | files]"
user-invocable: true
---

# Review

Review changes for correctness and actionable risk. Verify claims against primary evidence before judging the implementation. Use Chinese when communicating with users.

## Principles

1. **Verify the background**: establish the concrete problem, trigger conditions, affected behavior, and whether the problem is real.
2. **Prefer primary evidence**: rely on code paths, tests, command output, specifications, official documentation, and first-party project documentation.
3. **Separate facts from inferences**: label uncertainty and state what evidence is missing.
4. **Report actionable issues**: explain concrete impact and a recommended action; skip automated formatting/lint concerns and personal preferences unless they expose real risk.
5. **Respect project context**: follow applicable repository instructions and existing architecture, naming, error handling, logging, and test conventions. Do not request unrelated refactors.
6. **Review by default**: do not modify code unless the user explicitly asks for fixes.

## Workflow

### 1. Establish Scope and Background

- Identify the target: PR, branch diff, commit, issue, files, or code snippet. Clarify only material ambiguity; do not expand the scope without reason.
- For remote PRs or issues, use available repository or GitHub tooling to inspect the description, comments, linked items, checks, history, and diff.
- Determine the claimed problem, trigger conditions, affected users, and visible behavior. Do not treat the PR or issue description as proof.
- Verify the claim through code paths, tests, logs, existing behavior, history, or documentation. For external behavior, prefer official or first-party sources and cite the evidence. State anything that remains unverified.

### 2. Review the Approach

Check whether the change:

- Addresses the root cause rather than masking a symptom.
- Covers main paths, boundary conditions, error paths, failure modes, compatibility, migration, and rollback where relevant.
- Fits existing abstractions without bypassing helpers, validation, locking, transactions, permission checks, or safety mechanisms.
- Avoids unnecessary behavior changes and documents intentional user-facing changes.

### 3. Review Implementation and Tests

Evaluate the relevant dimensions:

- **Correctness and reliability**: logic, edge cases, error propagation, races, nil/bounds errors, resource leaks, transaction consistency, and recovery.
- **Simplicity and consistency**: directness, duplication, hidden side effects, existing helpers and patterns, and language conventions.
- **Performance**: avoidable I/O, lock contention, full scans, repeated network calls, memory amplification, and hot-path overhead.
- **Security**: permissions, authentication, path and input handling, sensitive data, data integrity, and recoverability.
- **Compatibility**: APIs, CLIs, configuration, serialized formats, cross-version behavior, and backend parity.
- **Tests**: reproduction of the original problem, fixed behavior, boundaries, errors, concurrency, compatibility, and backend-specific paths. Check that tests are stable, minimal, readable, and use the smallest relevant commands. If tests were not run, explain why and recommend them.
- **JuiceFS**: apply `AGENTS.md`, especially its requirements for POSIX semantics, data integrity, metadata-engine parity, dump/load and mixed-version compatibility, object-storage behavior, concurrency, and failure recovery.

### 4. Report the Review

Use the following three top-level sections, in this order. This gives the reader enough context to understand the review before presenting defects and risks.

```markdown
## Summary

## Solution

## Findings
```

In **Summary**:

- Summarize the reported problem, relevant background, trigger conditions, affected users or behavior, and expected behavior.
- State the verified root cause when the evidence supports one. Clearly distinguish verified facts from claims, assumptions, and unknowns.
- Keep this section concise and focused on the context needed to evaluate the change.

In **Solution**:

- Explain the implemented or proposed solution, its key mechanism, and the main code paths or components it changes.
- Describe why the approach addresses the problem, plus important compatibility, migration, performance, security, or design tradeoffs when relevant.
- Summarize the approach rather than narrating the diff file by file. Do not present the solution as correct before completing the review.

In **Findings**:

- List actionable findings ordered by severity. Each finding should use this structure:

```markdown
### [Severity] Title

- Location: `path:line`
- Problem: ...
- Impact: ...
- Evidence: ...
- Recommendation: ...
```

- After the individual findings, include assumptions or unknowns, tests run or not run, remaining risks, and a verdict when supported by sufficient evidence: Approve, Request changes, Comment only, or Need more information. Keep this supporting review context within the **Findings** section instead of creating additional top-level sections.

- If there are no findings, say so explicitly under **Findings**, then identify what was checked and what remains unverified.

## Severity

- **Critical**: likely data loss or corruption, permission bypass, security vulnerability, severe outage, or unrecoverable incompatibility.
- **High**: likely real-world wrong behavior, crash, major performance regression, or key feature regression.
- **Medium**: edge-case defect, compatibility risk, or test gap that may hide a real regression.
- **Low**: minor, non-blocking risk or maintainability issue; do not report pure style preferences.
