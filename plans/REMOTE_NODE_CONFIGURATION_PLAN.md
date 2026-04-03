# Remote Node Configuration Plan

Status: Draft

## Purpose

Define project-level configuration for remote node support.

## Current State

- The repo does not yet have a concrete plan for project-level remote node configuration.
- Remote node support overlaps with unmanaged localhost and SSH project support, but the project-level configuration model still needs its own design.

## Scope To Define

- project-level defaults for remote node behavior
- allowed remote providers or connection modes per project
- inheritance and override rules between global, project, and node settings
- interaction with monitoring, shells, and future runtime providers

## Open Questions

- Should remote node support be opt-in per project?
- Which connection and trust settings belong in project metadata versus external secrets/config?
- How do remote node defaults interact with project inheritance and sub-projects?
- How should project-level remote settings affect TUI action availability?

## Next Step

Expand this stub into a concrete implementation plan before starting project-level remote node configuration work.
