---
title: AI-assisted development
description: How AI tools were used in this codebase and what human review covered.
---

This project was developed with the assistance of AI coding tools. AI was used for
code generation, debugging, documentation and architecture decisions across the
whole codebase — the Go backend, the frontend dashboard, the Docker tooling and
the security analysis.

**All AI-generated code has been reviewed, tested and validated by a human**
before being merged. The project includes:

- A documented [security review](../reference/security.md) identifying and
  triaging potential vulnerabilities.
- Manual verification of AI-generated logic against Docker API behaviour.
- Review of all AI-authored documentation for accuracy.

Agents and tools working in this repository should read
[`AGENTS.md`](https://github.com/jordantrizz/dumbdock/blob/main/AGENTS.md), which
records the per-feature contracts, the build rules, and the known security
findings and their fixes.

## Related

- [Security review](../reference/security.md) — findings, severity and remediation.
- [Development](./development.md) — the conventions a contributor follows.
