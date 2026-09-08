# CodeLima 0.3.0 promotion verification

The maintainer requested promotion of the current published beta to a regular
release and removal of the separate Homebrew beta path on 2026-09-08.

Application code and native packaging are retained from `v0.3.0-beta.3`
(`1c146b71e0a43a29b84d7426caa02f210ed6428c`). Release tooling and documentation
change to accept regular tags only, require release notes, publish Latest,
update `Formula/codelima.rb`, and remove `Formula/codelima-beta.rb` atomically.
Historical beta tags and releases remain publication records.

## Automated regression evidence

Tests first failed against the previous channel policy for accepted beta tags,
beta formula generation, regular publication flags, and the tap promotion.
`make test-release` then passed after implementation. The workflow tests execute
the actual publication shell with a recording GitHub CLI and the actual tap
update against a disposable local Git remote. They verify regular/Latest flags,
all six assets, required release notes, stable formula replacement, beta removal,
and an idempotent second tap update.

Local Linux arm64 verification passed: `make verify`, `make test-race`,
`make test-integration`, and `make test-package`. `gopls check` reported no
problems in the changed Go files. `go doc` confirmed the release API, and the
locally built CLI ran successfully with `--version`. Tag metadata resolves
`v0.3.0` to `0.3.0`. Application and native packaging diffs against beta.3 are
empty.

The first full race run encountered a Git missing-tree-object error while
pushing to the test's disposable local tap. Ten focused race-enabled repetitions
passed, followed by a complete successful race/integration/package run. No
production repository was involved. An intermediate diagnostic run with global
`GOFLAGS=-race` also failed installer fixtures because those fixtures deliberately
compile without cgo; that diagnostic invocation was unsuitable and was replaced
by the standard race recipe. Native release workflow results follow publication;
all three native automated jobs must pass first.

## Remaining qualification

The previous [beta report](homebrew_beta_qa.md) records passed native automated
checks and public artifact validation. Its incomplete manual/native checks
remain open in TODO #41/#43/#44: actual Homebrew installation and upgrade,
Lima VM and forwarding flows, physical keyboard/graphics and multi-window
behavior, and the full upstream Ghostty Debug suite on a larger host.

The maintainer approved publication with those limitations and subsequently
explicitly requested regular promotion. That authorization changes publication
status, not the evidence or completion status of these remaining checks.

## Published result

- Regular release and GitHub Latest: [v0.3.0](https://github.com/brianrackle/codelima/releases/tag/v0.3.0).
- Annotated tag resolves to `c5311c836ce7990045d6c5ebb185355296014420`.
- [Release workflow 34287142410](https://github.com/brianrackle/codelima/actions/runs/34287142410)
  passed every job. Native verification, race, integration and package tests
  passed on macOS arm64, Linux amd64 and Linux arm64 before publication.
- The standard formula is version 0.3.0 in tap commit
  `a1c6c95a2f9cefa21da8e9344d59b3c0e92a7e5b`. The same commit deletes
  `Formula/codelima-beta.rb`. The regular formula is linked normally.
- All six public assets downloaded successfully. Archive digests match both
  GitHub asset metadata and manifests; formula URLs/checksums match all three
  targets. Each archive contains exactly the executable CLI and renderer worker.
- Native renderer build fingerprints match the published beta.3 for every target.
- `make test-package-artifact` passed against the downloaded Linux arm64 release,
  including the actual CLI and renderer startup with an empty runtime PATH,
  version/provenance checks, and terminal output/read verification.
- Verification downloads, the disposable tap checkout and all smoke-test state
  were removed. No verification processes remain. Development tools/builds are
  retained in their normal ignored directories.

| Target | Archive SHA-256 |
| --- | --- |
| darwin/arm64 | `b7d6b773fb9c9d2212ca8a9dc2d1dfcafcf92b94727f2478e1fb09681eb78007` |
| linux/amd64 | `1fa7a5986d0cb7532592ae869760df2487cad5e55464ff4c9a6a4a123c38a7dd` |
| linux/arm64 | `224f73464110bc2efc8d775a5d9666a02307bd785c063d8caf4a303ed942d230` |
