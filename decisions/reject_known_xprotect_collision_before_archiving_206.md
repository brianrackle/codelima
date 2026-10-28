# Reject the known XProtect collision before archiving

## Context and Problem Statement

macOS XProtect 5368 classified the v1.0.0 CLI as malware and moved it to Trash.
The evidence recorded in TODO #4 identifies ordinary adjacent Go strings that
match `XProtect_MACOS_FRIENDLYFERRET_SECD`. The port-row format participates in
the `cs%s%d` collision. Signing alone does not prove that this verdict is fixed.

## Decision Drivers

* Preserve displayed port rows and the native macOS capability adapter.
* Check the actual CLI and renderer payloads before publishing an archive.
* Keep local packaging independent of Apple credentials and external scanners.

## Considered Options

* Change row construction and reject the known bytes at the archive boundary.
* Depend exclusively on an external scan of the active Apple YARA rules.
* Add signing and notarization without a collision regression check.

## Decision Outcome

Chosen option: change row construction and reject the known bytes in both
Darwin executables in `release.BuildArchive`, before creating output. Compose
the row with decimal integer conversion and concatenation, preserving its text.
The existing archive callers and native release workflow inherit the check.

### Positive Consequences

* Both executable payloads are checked, including direct archive callers.
* Automated tests can reproduce rejection without macOS or a YARA dependency.

### Negative Consequences

* The check conservatively rejects the known bytes even without the full rule.
* It does not establish current XProtect, Gatekeeper, or Homebrew acceptance.

## Pros and Cons of the Options

The archive check is portable and covers every caller of `BuildArchive`.
An active-rule scan catches more detections, but requires current Apple
rule files and a scanner. It remains required native release qualification.
Developer ID signing and notarization improve distribution trust, but require
credentials and are separate from this false-positive regression.

## Links

* [Remaining macOS qualification](../TODO.md)
* [Apple malware protections](https://support.apple.com/guide/security/protecting-against-malware-sec469d47bd8/web)
