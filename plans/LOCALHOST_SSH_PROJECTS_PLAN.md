# Localhost And SSH Projects Plan

Status: Draft

## Purpose

Define support for projects whose nodes are unmanaged localhost or SSH connections rather than CodeLima-managed VM or container runtimes.

## Current State

- CodeLima assumes managed runtimes with workspace lifecycle under the control plane.
- There is not yet a dedicated plan for unmanaged connection-backed projects.

## Intended Shape

- a project can target localhost or SSH-accessible environments
- nodes represent unmanaged connections
- CodeLima does not own workspace creation, sync, or deletion for those nodes
- shell and monitoring behavior must work without managed workspace setup

## Scope To Define

- project metadata for localhost and SSH targets
- connection identity and credential references
- unmanaged node semantics
- CLI and TUI behavior differences from managed runtimes
- limits on operations such as clone, snapshot, bootstrap, and delete

## Open Questions

- Is localhost treated as a provider, a project mode, or a node mode?
- How should SSH hosts be addressed and authenticated?
- Which current node lifecycle operations remain valid for unmanaged connections?
- How should agent monitoring work when the workspace is not managed by CodeLima?

## Next Step

Expand this stub into a concrete implementation plan before starting unmanaged localhost or SSH project support.
