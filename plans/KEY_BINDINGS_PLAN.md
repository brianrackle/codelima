# TUI Key Bindings Specification

Status: Implementation in progress (Go; replaces the pre-manager draft).
Purpose: make the design's `settings.yaml › keys` promise real across dispatch,
contextual help, palettes and visible keyboard hints.

## 1. Problem and scope

The current manager has fixed keys spread across view, overlay and terminal
controllers. Its help catalogue repeats those keys. The earlier plan refers to
retired project controls and `_config/config.yaml`, and its case-insensitive
normalization would collapse distinct commands such as `t`/`T` and `x`/`X`.

Required coverage includes manager/global navigation; fleet and node-detail
commands; lifecycle, Metrics, Logs and Spec controls; Templates/Environments;
Ports, Activity and System; dialogs, selectors, command editors, palette, help,
confirmation/review overlays, and embedded-terminal focus/inspection controls.
Matching, help, palette shortcuts, buttons and footers must describe the same
effective bindings. Existing action guards and captured-target reviews remain
responsible for whether an action may run.

Ordinary text editing and bytes sent to a focused shell are input, not manager
commands. Raw attachment remains a separate real-terminal relay with its existing
`Ctrl+]` detach contract. Mouse interaction does not depend on a keyboard binding.

## 2. Assumptions and compatibility

* Use the current settings file and current manager action/context vocabulary.
* Preserve current default behavior, including `q` backing out of node detail and
  quitting at the top level. `Ctrl+c` is the separate application-quit action.
* F6 is the default embedded-focus key. If explicitly remapped, only configured
  focus keys are intercepted while the embedded shell owns input; other keys,
  including Option+Left/Right, continue to the shell unchanged.
* Missing overrides inherit catalog defaults. An explicit empty list disables an
  optional action; required escape/quit/focus actions cannot be left unbound.
* Keep authored overrides in settings, not a generated copy of every default.
  An inspection command exposes the complete effective/default map. This follows
  the existing minimal, comment-preserving settings writer and permits future
  defaults to evolve without overwriting user choices.
* No key-sequence/chord language or executable expressions are introduced. Each
  configured item identifies one key plus modifiers.

## 3. Domain model and identifiers

`KeyBindingConfig` is a map from stable dotted action IDs to ordered key lists.
An action definition contains its ID, description, defaults, active contexts,
whether it is required, and whether it is available during text entry. Contexts
identify actual focused surfaces, including detail panels and input modes; they
must be explicit enough to represent mutually exclusive commands.

The compiled keymap is immutable. It owns normalized bindings, indexes by context
and key identity, and display metadata. Public inspection returns copies. Runtime
controllers resolve semantic action IDs and retain the original `vaxis.Key` for
press/repeat/release ownership; keys must not be rewritten into synthetic default
keys. A new binding must not inherit accidental behavior from an old key's branch.

## 4. Settings and normalization contract

```yaml
keys:
  node.delete: [ctrl+d]
  node.clone: [c, alt+c]
  navigation.up: [up, k]
```

Keys are case-sensitive logical characters. Modifier and named-key words are
case-insensitive and normalized to a stable spelling. `T` and `shift+t` identify
the same key; `t` is different. Supported named keys include arrows, Tab, Enter,
Escape, Space, Backspace, Delete, Insert, Home, End, PageUp/PageDown and F1–F63.
Single printable noncombining Unicode characters are allowed. `plus` names `+`
inside a modified binding. Unknown names/modifiers, control characters, multiple
characters and empty tokens fail validation rather than matching a first rune.
CapsLock/NumLock are event state, not binding modifiers.

Normalize common legacy aliases (`ctrl+i`/Tab, `ctrl+m`/Enter,
`ctrl+[`/Escape, `ctrl+h`/Backspace) to one collision identity. Shifted printable
symbols must not produce duplicate aliases. Event matching prefers actual text
for unmodified printable input, then shifted/key codes, with a base-layout
fallback when needed. Printable case must remain distinct with both text-only
legacy events and Kitty events. Paste never resolves to an action.

Each action accepts at most eight bindings and each spelling at most 64 bytes.
Unknown action IDs, equivalent duplicate bindings, and collisions between
*different* actions active in the same context are invalid. Reusing a key in
mutually exclusive contexts is valid. Errors identify the setting/action,
spelling and conflicting context/action. Ordering is deterministic.

Actions available during text entry require nonprinting or modified bindings;
unmodified printable global/view commands are absent from input contexts.
Native text-widget editing keys and pasted text retain their existing behavior.

## 5. Configuration lifecycle and recovery

1. Load `_config/settings.yaml` and compile a complete binding snapshot before
   applying it. Compilation must not mutate the raw config, catalog or prior map.
2. At initial TUI startup, invalid binding overrides produce a visible diagnostic
   and the safe default map. Lifecycle/daemon commands must remain usable because
   a frontend preference is invalid.
3. On an explicit preferences refresh or successful settings write, apply a valid
   replacement atomically. On invalid authored overrides, retain the last good
   map and show a deduplicated diagnostic; help reports the effective map, never
   the rejected values as though they were active.
4. Preserve held physical-shortcut claims through a map change. Repeats/releases
   of a transition key cannot enter a newly exposed text field or shell.
5. `settings set keys.<action> '<YAML list>'` validates the prospective complete
   map under the settings lock before writing. The dotted action ID is one map
   key. Preserve comments, unknown settings and unrelated overrides.
6. `settings keys` exposes action/context/default/effective bindings and any
   binding-validation error. `settings reset keys [action]` removes authored
   overrides for recovery without requiring the invalid map to compile first.
   Malformed YAML is still refused, never silently replaced by these commands.

Persist only settings data. The keymap belongs to each frontend; no daemon or
renderer protocol change is needed. Older builds already preserve unknown
settings fields. No keyboard configuration may contain or execute shell code.

## 6. Dispatch and rendering invariants

* An event resolves in its actual focused context before its action runs.
* Actions use one shared registry. Context-disjoint meanings of the same default
  key stay distinct; shared meanings use one action ID across surfaces.
* Existing press-only actions remain press-only. Navigation/text editing may
  repeat. Paste/release cannot confirm, delete, attach or switch views.
* Physical lifecycle tracking continues to use the real triggering key.
* Every displayed configurable key comes from the effective registry. Compact
  hints may show a primary binding plus an alias count; help shows all bindings.
  Disabled actions display an unbound indication and remain available by mouse
  or palette where those paths already exist.
* Help/search are read-only; selecting or searching a binding never invokes it.
* Raw-shell payload, literal text entry and operating-system terminal shortcuts
  are not globally remapped by manager settings.

## 7. Validation matrix

Automated checks must cover strict parsing and normalization, Unicode/case and
legacy aliases, overlapping/disjoint contexts, required/disabled actions, limits,
copy isolation, and deterministic diagnostics. Verify real dispatch with a
nondefault destructive key opening its captured review, old keys deactivated,
aliases, context-specific reuse, and matching effective help/footer/palette text.

Exercise press/repeat/release/paste across remapped view/form/terminal transitions,
including modifiers released before the triggering letter. Verify that text
entry receives ordinary characters and that embedded input keeps Option word
navigation. Check invalid startup/reload recovery, settings round trips, CLI
reset and comments/unknown-key preservation. Add no test that only repeats the
registry's data; assert observable behavior at dispatch/rendering boundaries.

Live QA must use an isolated home with custom mappings, show their help/hints at
80×24 ANSI and 150×38 truecolor, exercise view/detail/form/menu/review actions,
prove old keys no longer act, and test real embedded-shell input/focus. Restart
and refresh must retain valid bindings and recover from an invalid authored map.
Cancel destructive reviews. Remove every owned verification artifact; put Unix
sockets on a project-rooted native/tmpfs directory, not the virtiofs share.

## 8. Implementation checklist

- [x] Strict parser, immutable compiler and context/collision model.
- [x] Complete action/context inventory for all required surfaces.
- [x] Settings storage, inspection, validated update and recovery commands.
- [x] Manager/detail/global dispatch and physical lifecycle integration.
- [x] All overlays, menus, forms and embedded focus/inspection dispatch.
- [x] Effective help, palette, footer and button hints.
- [x] Invalid-map recovery and atomic refresh with retained gesture ownership.
- [x] Automated gates, live QA, documentation/ADR and verification cleanup.

The remapping promise is not delivered by completing only the parser or manager
scope. Keep the Help artboard/roadmap partial until every listed surface is wired
and validated.
