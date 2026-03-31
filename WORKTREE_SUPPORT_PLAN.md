# Worktree Support Plan

Status: Draft

## Purpose

Define how CodeLima configuration and project semantics should support Git worktrees cleanly.

## Current State

- The repo does not yet have a concrete implementation plan for worktree-aware configuration and workflows.

## Scope To Define

- how projects identify worktree-backed workspaces
- how snapshots and lineage interact with worktrees
- how copy and mounted workspace modes should behave
- whether project identity attaches to the repository root, the worktree path, or both
- how TUI and CLI surfaces should present worktree context

## Open Questions

- Should multiple worktrees under one repository be separate projects or a grouped project family?
- How should default paths and inherited settings behave across sibling worktrees?
- How should node naming and cloning behave for worktree-backed projects?
- Do unmanaged remote projects need worktree-aware configuration too?

## Next Step

Expand this stub into a concrete implementation plan before starting worktree support changes.
