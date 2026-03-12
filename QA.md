# QA

## Shell Verification

This flow verifies that `codelima shell` enters the node in the mounted project workspace instead of inheriting an unrelated host working directory.

Prerequisites:

- build the binary with `make build`
- ensure a running node named `design` exists

Non-interactive verification:

```sh
./bin/codelima shell design -- pwd
```

Expected result:

- command exits successfully
- output is `/Users/brianrackle/Projects/test_lima/test-project-dir`

Interactive verification:

```sh
./bin/codelima shell design
```

Inside the shell run:

```sh
pwd
exit
```

Expected result:

- `pwd` prints `/Users/brianrackle/Projects/test_lima/test-project-dir`
- `exit` returns cleanly to the host shell
