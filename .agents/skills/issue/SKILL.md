---
name: issue
description: "Analyze issues, bug reports, and feature requests. Clarify the problem, check the evidence, and give a concise conclusion and next step."
---

## Analysis

- Follow trusted `AGENTS.md` and contribution guidelines. Read the issue and relevant discussion; check the reported behavior against the affected version, code, documentation, and related issues or PRs.
- Separate the reported symptom, verified behavior, and suspected cause. An unverified root cause or missing reproduction does not disprove the problem. Ask only for missing information that changes the conclusion or next action.
- For feature requests, establish the actual use case and why existing behavior is insufficient before discussing implementation. Identify duplicates or existing fixes only when supported by evidence.

## Output

Respond in Chinese, translating the labels below and using this order. Keep each item brief, with short sentences and clear actors. Skip introductions, investigation diaries, and repeated context.

- **Problem**: Explain the reported problem, when it occurs, and its impact in one or two short sentences. For requests, state the use case and current limitation.
- **Assessment**: Say whether this is a defect, expected behavior, or needs more evidence or discussion. Qualify uncertainty and give a root cause only when supported.
- **Evidence**: Link only decisive evidence next to the claim it supports. State verification scope once; distinguish author reports, code-based conclusions, and independent reproduction.
- **Next step**: Give one concrete action, with the most important check or missing fact when needed.

Default to analysis only. Do not modify files, post comments, or change GitHub state unless the user requests it.
