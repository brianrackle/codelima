# Sub-Project Plan

Status: Draft

## Purpose

Define how CodeLima should represent, create, display, and manage sub-projects beneath existing projects.

## Current State

- The project model already supports hierarchy structurally.
- There is not yet a dedicated implementation plan for sub-project UX, lifecycle, lineage behavior, or TUI interactions.

## Scope To Define

- creation and deletion rules for child projects
- parent and child workspace relationship rules
- lineage and snapshot behavior
- project tree rendering and actions
- interaction with environment configs, nodes, and inherited defaults

## Open Questions

- Should sub-projects require workspace containment under the parent workspace?
- What settings inherit from the parent versus copy at creation time?
- How should node creation defaults behave for sub-projects?
- How should destructive operations handle parents with child projects?

## Next Step

Expand this stub into a concrete implementation plan before starting sub-project work.
