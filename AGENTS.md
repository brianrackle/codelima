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

Instructions for how to setup the development environment, run the project, run the tests, and run other tooling will be documented in the README.md

The README.md must also stay current with an outline of the user-facing capabilities and a user guide that includes practical examples for common workflows and useful command invocations.

When the `readme` skill is available in the current session, it should be used to maintain the README.md.

All tooling should be captured as make recipes

The makefile will include an `init` command that will create the sandboxed development environment that agents can use.

The `.gitignore` should be kept up to date so generated files, caches, downloaded dependencies, and local scratch artifacts do not pollute the git worktree.

If any tools dont run in the sandbox and need approval the sandbox then the environment needs to be updated so that tooling will run in the sandbox.
