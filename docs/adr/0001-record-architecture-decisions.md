# 1. Record architecture decisions

- Status: accepted
- Date: 2026-11-01

## Context

The project makes modelling and design choices (which physical models, which
conventions, which dependencies) whose reasons are easy to lose. Contributors
and users need to see why a choice was made, not only what the code does.

## Decision

Significant decisions are recorded as Architecture Decision Records in
`docs/adr/`, using the format described by Michael Nygard in
[Documenting Architecture Decisions](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions).
Each ADR is numbered, immutable once accepted, and superseded rather than
edited when a decision changes.

## Consequences

Decisions are reviewable in pull requests like code. Changing a recorded
decision requires a new ADR that explains what changed and why.
