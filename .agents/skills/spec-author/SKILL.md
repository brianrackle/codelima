---
name: spec-author
description: Create or revise implementation-ready engineering specifications, design specs, and internal technical contracts. Use when the user wants a serious spec for a feature, subsystem, workflow, service, protocol, or platform change; when an existing draft spec needs to be tightened; or when requirements need to be clarified before drafting a spec.
---

# Spec Author

Use this skill when the user wants an implementation-oriented technical specification rather than product copy, brainstorming notes, or lightweight documentation.

## What This Skill Produces

Produce a spec that is:

- structured for implementers and reviewers
- explicit about scope, invariants, failure behavior, and operator concerns
- concrete enough that engineering work can begin from the document
- careful about separating required behavior from recommendations and future work

Do not produce marketing prose, shallow project briefs, or vague design notes.

## Workflow

### 1. Clarify the decision surface first

Ask focused questions when the request leaves important ambiguity about:

- system purpose and actors
- in-scope versus out-of-scope behavior
- lifecycle and major workflows
- dependencies and external systems
- config and policy decisions
- data model and state transitions
- failure and recovery behavior
- observability and operational needs
- trust boundaries and safety constraints
- implementation expectations such as target stack or required artifacts

Ask only what materially affects the contracts you need to write. If the user already provided enough context, do not stall on formal discovery.

### 2. State assumptions when needed

If some details remain unknown after reasonable clarification:

- add a short `Assumptions` section near the top
- make assumptions conservative and implementation-useful
- never silently invent critical requirements

### 3. Draft the spec

Unless the user asks for another format, use this structure and omit sections that are truly irrelevant:

```markdown
# <System Name> Specification

Status: Draft v1 (<stack or language guidance>)
Purpose: <1 sentence>

## 1. Problem Statement

## 2. Goals and Non-Goals
### 2.1 Goals
### 2.2 Non-Goals

## 3. System Overview
### 3.1 Main Components
### 3.2 Abstraction Levels
### 3.3 External Dependencies

## 4. Core Domain Model
### 4.1 Entities
### 4.2 Stable Identifiers and Normalization Rules

## 5. Workflow / Protocol / Contract Specification
### 5.1 Entry and Invocation
### 5.2 Request / Message / Input Rules
### 5.3 Config or Schema Surface
### 5.4 Processing / Execution Contract
### 5.5 Validation and Error Surface

## 6. Configuration Specification
### 6.1 Source Precedence and Resolution
### 6.2 Change Semantics
### 6.3 Preflight Validation

## 7. State Machine / Lifecycle
### 7.1 Internal States
### 7.2 Session or Attempt Lifecycle
### 7.3 Transition Triggers
### 7.4 Idempotency and Recovery Rules

## 8. Integration Contract
### 8.1 Required Operations
### 8.2 Normalization Rules
### 8.3 Error Handling Contract
### 8.4 Boundary Notes

## 9. Logging, Status, and Observability

## 10. Failure Model and Recovery Strategy

## 11. Security and Operational Safety

## 12. Reference Algorithms

## 13. Test and Validation Matrix

## 14. Implementation Checklist
```

Adapt headings to the actual problem. For small specs, collapse sections rather than padding the document.

## Writing Rules

Prefer:

- precise, engineering-first prose
- explicit normative language such as `must`, `should`, `may`, `required`, and `implementation-defined`
- named invariants and validation rules
- concrete entity fields, states, and transitions
- specific failure classes and recovery behavior
- explicit config precedence and defaults
- operational surfaces operators can inspect

Avoid:

- vague phrases like "handle appropriately" without defining behavior
- burying important constraints inside incidental prose
- pretending uncertain areas are fully specified
- copying user wording when it is underspecified or self-contradictory

## Quality Bar

Before finalizing, check that the spec:

- clearly defines the problem, goals, and non-goals
- distinguishes required behavior from optional extensions
- names important entities and state transitions
- defines failure handling and operator recovery paths
- covers configuration, validation, and observability where relevant
- is specific enough that an engineer could implement against it

## Output Style

- Use Markdown only.
- Keep prose dense and high signal.
- Use bullets for enumerations, field lists, invariants, and rules.
- Use code blocks for examples, payloads, or pseudocode when they materially help.
- Do not add fluff or meta-commentary about being an AI.
