# Use One Regular Homebrew Release Channel

## Context and Problem Statement

ADR 135 introduced a separate keg-only beta formula for the libghostty branch.
After publishing `v0.3.0-beta.3`, the maintainer requested removing that path and
promoting the current beta to a regular release. Existing installations should
receive this version through `brew upgrade codelima`.

## Decision Drivers

* Keep a single installation and upgrade command.
* Preserve the application code and native packaging verified for beta.3.
* Carry forward the recorded qualification gaps without claiming they passed.

## Considered Options

* Maintain both formulae after promoting the beta.
* Publish a regular release and retire the separate beta channel.

## Decision Outcome

Chosen option: "Publish a regular release and retire the separate beta channel",
as explicitly requested by the maintainer. Release metadata now accepts only
`vMAJOR.MINOR.PATCH`. The workflow creates regular Latest releases with required
versioned release notes and updates only `Formula/codelima.rb`. It removes the
retired beta formula in the same tap commit, with an idempotent removal so later
releases work when that file is already absent.

`v0.3.0` promotes beta.3's application code. Native builds still run all automated
verification gates. The previously approved publication exception and explicit
promotion request do not complete the remaining manual/native QA in TODO
#41/#43/#44; the regular notes disclose those gaps. Historical beta releases and
tags remain intact as publication records.

Existing beta users install or upgrade regular `codelima`, update the daemon
using its CLI, then uninstall the old beta keg and remove beta-specific PATH
configuration. CodeLima's existing home and daemon update mechanism are reused.

### Positive Consequences

* Existing regular installations receive the promoted code through normal upgrades.
* One formula and command reduce installation and daemon-version confusion.
* Publication notes and exact-tag builds preserve qualification evidence.

### Negative Consequences

* Installed beta kegs need explicit cleanup by their owners.
* Regular users receive a release with the disclosed manual QA still incomplete.
* Introducing any future prerelease channel requires another explicit decision.

## Pros and Cons of the Options

### Maintain both formulae after promotion

* Good, because existing beta installations can remain on their selected path.
* Bad, because it retains the parallel installation the maintainer rejected.

### Publish a regular release and retire the separate beta channel

* Good, because it restores one normal Homebrew upgrade path.
* Bad, because beta users must remove their old keg and PATH customization.

## Links

* Supersedes [ADR 135](isolate_homebrew_beta_releases_135.md).
* [Release process](../BUILD.md).
* [Promotion verification](../plans/regular_release_qa.md).
