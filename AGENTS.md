Every change will be fully tested with automated tests, and the tests will pass for the change to be considered completed.

Test driven development is preferred.

Go will be used as the primary language.

The Go toolchain and Go modules will be used to manage dependencies and run the code.

Linters and formatters should be used to keep the code maintainable, using gofmt and golangci-lint.

Code will be written in an idiomatic way.

Code will be written for production quality.

SOLID principals will be followed.

Best practices will be used.

Reusable will be documented in a PATTERNS.MD file and reused to reduce copy pasta.

When work is complete the code needs to be run and verified locally.

Manual verification must be performed for every verification flow defined in QA.md.

Any scoped-out, deferred, or partially completed follow-up work must be recorded in `TODO.md` before moving on.

If you switch to another task while the previous task still has meaningful follow-up work available, document that remaining work in `TODO.md` with the problem, a suggested solution, and the main advantages and disadvantages.

Items in `ROADMAP.md` must be marked complete when finished, or marked partially complete when only part of the roadmap item has been delivered.

Any change that affects how the system works internally, such as runtime integrations, rendering behavior, storage layout, or architecture, must be documented as a numbered Architecture Decision Record in `decisions/` using `ADR_TEMPLATE.md`. Product-surface changes like adding a new command do not require an ADR unless they also change the system architecture.

Keep temporary work local to the project under a project-rooted temp directory such as `./tmp/` instead of using system temp directories.

Any artifacts created during manual testing or manual verification must be cleaned up before considering the task complete. This includes temporary files and directories, test data stores, packaged outputs, disposable infrastructure or service instances created only for verification, and any other verification-only environment state.

Instructions for how to setup the development environment, run the project, run the tests, and run other tooling will be documented in the README.md

The README.md must also stay current with an outline of the user-facing capabilities and a user guide that includes practical examples for common workflows and useful command invocations.

When the `readme` skill is available in the current session, it should be used to maintain the README.md.

The BUILD.md must be kept up to date with the maintainer-facing build, packaging, and release process.

All tooling should be captured as make recipes

The makefile will include an `init` command that will create the sandboxed development environment that agents can use.

The `.gitignore` should be kept up to date so generated files, caches, downloaded dependencies, and local scratch artifacts do not pollute the git worktree.

If any tools dont run in the sandbox and need approval the sandbox then the environment needs to be updated so that tooling will run in the sandbox.
