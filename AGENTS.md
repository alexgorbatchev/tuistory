---
created_on: 2026-10-02 06:50
last_modified: 2026-10-02 22:46
status: current
---

# tuistory Developer & Agent Instructions

This project requires Go 1.26.8 or newer and uses `just` for task automation and Cobra with `cobra-help-tree/v2` for CLI commands. The Go 1.26 patch requirement includes the upstream Darwin race-detector fork fix ([Go issue 79806](https://github.com/golang/go/issues/79806)); keep CI's toolchain selection grounded in `go.mod`.

## Core Commands

```bash
# Build compiled binary into bin/tuistory
just build

# Run all unit, integration, and E2E tests
just test
# or: go test -v ./...

# Run static analysis and tests in sequence
just check

# Run static analysis / linter
just lint
# or: go vet ./...

# Format Go source files
just fmt

# Check module hygiene
go mod tidy -diff
```

## Running CLI Locally

Build the binary to `bin/tuistory` using `just build`.
Run commands directly via `./bin/tuistory <args...>` or `just run <args...>`.

**ALWAYS stop the daemon before manually testing any code change.** The relay daemon is a long-lived background process that keeps the **old compiled code** in memory across CLI invocations. Changes to handlers, session logic, or CLI parsing in the daemon process will appear to be ignored if an old daemon is running.

Run this before every local smoke test:

```bash
./bin/tuistory daemon-stop
```

The next `./bin/tuistory <command>` will spawn a fresh daemon with your updated binary.

The test suite handles this automatically: `e2e_test.go` reserves a dynamic port separate from the default `19977` and stops its owned daemon in `TestMain`. You do **not** need to manually stop daemons before running `just test`.

## Canonical CLI Syntax

Always use `tuistory -- <command>` instead of `tuistory launch "<command>"`. The `--` form is shorter and avoids quoting issues with nested commands. `launch` is an alias for the same thing, but `--` is the canonical syntax.

```bash
# Canonical
tuistory -s myapp -- ./server
tuistory -s dev -- kimaki tunnel -- ./server

# Discouraged
tuistory launch "./server" -s myapp
```

In Go, `os.Args` preserves `--` exactly. Options must precede `--`; everything after `--` is passed verbatim to the child command.

## Embedded Agent Skill

- `tuistory skill` prints `cmd/tuistory/SKILL.md` verbatim, embedded with `//go:embed`.
- Every agent-mode help screen starts with an alert to read `AGENT=1 tuistory skill` first.
- Keep `cmd/tuistory/SKILL.md` and `skills/tuistory/SKILL.md` in sync in the same change whenever public commands, arguments, flags, shorthands, types, defaults, accepted values, environment variables, outputs, errors, or side effects change.
- Verify against implementation and run `just check`. Maintain unit and E2E test coverage of the live command tree.

## Migration Repair Workflow

- Address every migration-review finding and due-diligence item; add newly verified defects to the repair scope and fix them.
- Maintain `.tmp/migration-fixes-checklist.md` with all findings and validation evidence. Check off items only after their verified fixes land on `main`, recording the landing commit.
- Coordinate parallel agents in isolated `.workspaces/` worktrees with explicit file ownership. Integrate and verify their commits before landing them on `main`.
- The current migration repair authorizes the reviewed public CLI behavior corrections and verified associated defects; it does not authorize releases or deployments.
- The user approved `golang.org/x/mod/semver` (`golang.org/x/mod v0.41.0`) for daemon semantic-version comparison during this repair.

## Boundaries

### Always
- Any time code is changed such that results from running that code are changed, a test file must be changed as well, and 90% code coverage is required (the `scripts/` folder is explicitly excluded from this rule).
- Automatically record all new user instructions in the most appropriate `AGENTS.md` file upon receipt (and check first if conflicts exist).
- Run `just check` (static analysis, race tests, and coverage enforcement) and `go mod tidy -diff` before declaring work complete.
- `just check` also runs race detection and enforces at least 90% statement coverage by combining fresh unit-test coverage with instrumented compiled CLI/daemon execution. Inspect `.tmp/coverage.out` and `.tmp/coverage-merged/`; never combine artifacts from different revisions.
- Isolate temporary files to `.tmp/` within the project root instead of global `/tmp`.

### Ask First
- Modifying public CLI argument structures, subcommands, or default behaviors.
- Introducing new third-party dependencies outside the researched and approved stack.

### Never
- Never publish releases, tags, packages, or production deployments automatically without explicit user authorization.
- Never commit compiled Go binaries (e.g. `bin/tuistory`, `*.test`) or test artifacts to git.
- Never use heredocs (`<<EOF`) for any reason.
