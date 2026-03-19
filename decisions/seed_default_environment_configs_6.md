# Seed Default Environment Configs During Store Bootstrap

## Context and Problem Statement

Reusable environment configs were previously empty in every new `CODELIMA_HOME`, so users had to create common bootstrap installers such as Codex and Claude Code by hand before they could assign them to projects. The system already seeds built-in agent profiles during store layout, so the question is where these default environment configs should be introduced without making the TUI and CLI diverge.

## Decision Drivers

* Fresh installs should expose useful reusable environment configs immediately.
* The CLI and TUI should see the same defaults without duplicating hardcoded lists.
* Seeded defaults should still behave like normal user-managed configs.
* Existing homes should gain the defaults automatically when they are first used after upgrade.

## Considered Options

* Seed built-in environment configs during store bootstrap.
* Hardcode built-in environment configs only in the TUI selectors and dialogs.
* Require users to create Codex and Claude Code configs manually from documentation.

## Decision Outcome

Chosen option: "Seed built-in environment configs during store bootstrap", because it makes the defaults available uniformly to the CLI and TUI, works for both new and existing homes, and reuses the same persistence pattern already used for built-in agent profiles.

### Positive Consequences

* Fresh and existing homes automatically gain `codex` and `claude-code` reusable environment configs.
* Project create and update can reference the defaults immediately from both the CLI and TUI.
* The seeded configs live in the normal metadata store, so list, show, update, delete, and selector flows need no product-specific branching.

### Negative Consequences

* Default command updates will not automatically overwrite homes where users already edited or deleted the seeded configs.
* Store bootstrap now owns another class of seeded metadata, which slightly increases initialization responsibility.
* Built-in config slugs become part of the long-lived compatibility surface.

## Pros and Cons of the Options

### Seed built-in environment configs during store bootstrap

Write `codex` and `claude-code` into the metadata store from `EnsureLayout` when those slugs do not already exist.

* Good, because the CLI and TUI both consume the same persisted records.
* Good, because existing homes get the defaults without a manual migration step.
* Bad, because later command changes do not propagate automatically once a config has been customized or deleted.

### Hardcode built-in environment configs only in the TUI selectors and dialogs

Expose synthetic defaults in the TUI without persisting them in the metadata store.

* Good, because it avoids changing store bootstrap.
* Good, because the UI could present curated labels or grouping.
* Bad, because the CLI would not see the same defaults unless it duplicated the same special cases.
* Bad, because synthetic configs would need separate mutation and persistence rules.

### Require users to create Codex and Claude Code configs manually from documentation

Keep the current store behavior and rely on examples in the README.

* Good, because it avoids any internal behavior change.
* Good, because there is no seeded metadata to maintain.
* Bad, because common setup remains repetitive.
* Bad, because the TUI and CLI selectors stay empty on first use.
