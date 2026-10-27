# ADR wp7-teaching-comments: Longer comments in teaching code

- **Status:** accepted.
- **Date:** 2026-10-27

## Context

`CLAUDE.md` limits new and changed comments to two lines. The recipe code that
`docs/usage.md` quotes is read as a guide, and with short comments it was
hard to follow.

## Decision

Teaching code is exempt from the two-line limit: runnable examples
(`Example...` functions) and the recipe code that `docs/usage.md` quotes.
Its comments may explain each step at more length. They still never
restate the code. All other code keeps the limit.
