---
created_on: 2026-10-05 13:45
last_modified: 2026-10-05 14:10
status: current
---

# Implementation audit

**Verdict: not ready.** Twelve findings remain: five high and seven medium. These include shutdown of an unrelated process, restart and passthrough failures under valid PATH overrides, an ineffective attach kill action, and false idle success. Required automated checks pass; independent probes reproduce these defects in the compiled implementation.

## Scope and evidence boundaries

The user selected the repository root for a full implementation audit. The audited revision is `31b03bebff37fa3ada24b5167b314ca5c4592f3e`, branch `main`. Initial staged, unstaged, and untracked Git status was clean. Inspection covered all 111 tracked files: 39 production Go files, 44 Go test files, nine font binaries, four font license texts, and the remaining documentation, manifests, configuration, automation, and license files. Font assets were exercised through the actual embedded-font parser and renderer, rather than inspected as source text.

During the audit, this report was the only addition outside ignored scratch/build directories. Production files were not edited. There were no commits, tags, releases, deployments, or tests against production services. Probes owned fresh dynamic ports, child processes, and files under `.tmp/implementation-audit/`; their daemons and fixture children were stopped. No existing daemon on port 19977 was targeted. Subsequent issue filing is recorded below and in the separate issue index.

Audit performed on Linux/amd64 with Go 1.26.8, just 1.58.0, Bun 1.4.0, and GoReleaser 2.15.4. Timestamp: 2026-10-05, America/Los_Angeles. Native macOS and Linux/arm64 execution were unavailable; cross-compilation succeeded. Findings refer to the audited source revision, not the older published release.

Requirements came from [AGENTS.md](../../../AGENTS.md), [README.md](../../../README.md), the [embedded operational guide](../../../cmd/tuistory/SKILL.md), [go.mod](../../../go.mod), the command/flag metadata and protocol types, and the invoked audit and applicable Go, CLI, review, and documentation skills. The ignored migration checklist was used only as a lead to re-inspect current code. Historical completion claims were not accepted as verification.

The applicable CLI skill explicitly requires a complete embedded operational reference, including generated help/completion commands, and tests comparing that reference with the live command tree. Its source is the shared `cli-best-practices` agent skill, “Embedded Skill Contract.” Project-specific canonical command syntax takes precedence over that skill's generic hierarchy preference.

## Requirements matrix

Evidence codes: **C** = fresh `just check`, including the named behavioral tests; **P** = isolated compiled CLI/daemon probes in `probe.log`; **B** = isolated boundary probes in `boundaries.log`; **S** = source inspection; **M** = independent module/build/configuration commands. Log paths and exact commands are recorded below. A verified row covers its stated acceptance check, not every possible input.

| Requirement and source | Implementation | Observable acceptance check | Evidence | Status |
| --- | --- | --- | --- | --- |
| Go >=1.26.8; AGENTS/go.mod | go.mod, CI toolchain selection | Installed toolchain satisfies requirement; build succeeds | Go 1.26.8; M | verified |
| Required vet/race/coverage check; AGENTS | justfile, scripts/coverage.sh | `just check` exits 0, coverage >=90% | C: 92.50% | verified |
| Module hygiene; AGENTS | go.mod/go.sum | `go mod tidy -diff` has no diff; downloaded modules verify | M | verified |
| Standalone builds for Linux/macOS amd64/arm64; release configuration | .goreleaser.yml, platform source files | All four targets compile | M; native execution limits below | verified |
| Release waits for Linux/macOS verification; AGENTS | .github/workflows/release.yml, verify.yml | Release cannot run before both native verification jobs pass | S: dependency graph present; actual hosted enforcement not checked | unverified |
| Canonical `--` preserves argv; AGENTS/SKILL | app/launch.go, session.New | Spaces, dollar signs, shell characters remain literal on initial launch | C: dash passthrough tests; P: helper argument | verified |
| Positional `launch` explicitly accepts shell source; SKILL | app/LaunchCommand | Explicit shell command executes, propagates child status in passthrough | C: TestClientOfflineAndPassthrough | verified |
| Caller cwd, relative cwd, environment, dimensions; SKILL | app/commands_launch.go, session.New | Real child observes configured cwd/env and initial PTY size | C: E2E_RelativeCwd, CallerEnvInherited, launch dimensions | verified |
| Caller PATH and relative entries are used in daemon mode; SKILL:76 | session/process_resolution.go | Helper outside daemon PATH launches using override and cwd | C: ReviewCallerPATH, process resolution tests; P | verified |
| Requested cwd/env work in foreground passthrough; SKILL:76,80 | cmd/runPassthrough | PATH override chooses the executable, including nested launch | P: override fails, absolute control succeeds; IA-003 | violated |
| Duplicate/concurrent launches reuse one live owner; SKILL | relay/registry.go | Concurrent callers produce one child; failed starts release reservations | C: LaunchOwnership, registry ownership/lifecycle tests | verified |
| Restart preserves identical launch arguments/cwd/env/dimensions; SKILL:74,234 | app/restartOptions.prepare, session metadata | Restart directly launched helper under same restricted PATH | P: launch works; restart loses session and fails; IA-002 | violated |
| Silent startup warns while keeping live child; SKILL | commands_launch.go, commands_lifecycle.go | No initial data does not destroy a live session; immediate exit reports error | C: startup and migration CLI tests | verified |
| Snapshot retains scrollback and valid Unicode/cursor cells; SKILL | session/output.go | Wide/combining characters and cursor positions render without byte splitting | C: SnapshotCellCoordinates, SnapshotIncludesScrollback | verified |
| Snapshot style/color filters reflect terminal attributes; SKILL | session/output.go, screenshot/CellColor | Nonmatching cells become spaces; explicit RGB and palette values match | C: SnapshotColorFilters, EmptyAndStyledSnapshots | verified |
| Non-immediate snapshots/screenshots wait for idle; SKILL | Text, commands_screenshot.go, WaitIdle | Continuous output cannot produce a false idle condition | S: callers ignore nil timeout result; P: idle failure; IA-005 | violated |
| `read` advances incremental cursor; `--all` does not; SKILL | session/Read, ReadAll, commands_read.go | Repeated reads and full-buffer reads have expected cursor behavior | C: E2E_ReadCommands, WaitForUnreadOutput | verified |
| `read` strips ANSI escapes; SKILL:115 | StripANSI, Read | Escape sequence split across incremental reads never leaks bytes | B: partial escape and final `m` leak; IA-010 | violated |
| Output retention is bounded to 1 MB; README/SKILL | session/session.go, output.go | Real buffered output is trimmed and read offsets remain valid | C: output retention tests; S | verified |
| Plain wait patterns match literally; SKILL:133 | session/Text, WaitForText | Wait for printed `█` succeeds | B: visible glyph still times out; IA-008 | violated |
| Regex flags i/m/s/g, malformed/unsupported expressions; SKILL | session/pattern.go, wait/click handlers | Valid flags match; errors occur before input side effects | C: pattern tests, ReviewRegexFlags, app regressions | verified |
| Wait/read unblock on output, exit, closing, or timeout; SKILL | session/output.go | Early crash and closing cancel waits; output-before-exit is drained | C: WaitExitsEarlyOnCrash, ImmediateOutputBeforeExit, waiter tests | verified |
| `wait-idle` waits for ~200 ms without PTY data, within maximum timeout; SKILL:135 | WaitIdle, newWaitIdleCommand | Busy child times out with a failure rather than `OK` | P: `OK` at 55 ms under continuous output; IA-005 | violated |
| Type sends text, validates exited/closed even for empty text; SKILL | session/Type, commands_input.go | Real writes and lifecycle rejection | C: input validation and close-state tests | verified |
| Press sends advertised key chords, including navigation modifiers; SKILL:148 | keys/GetKeyCode, session/Press | Child receives distinct Ctrl-left and Shift-up sequences | B: unmodified bytes; IA-007 | violated |
| Click resolves patterns, ambiguity, visible-cell coordinates; SKILL | session/Click | Unicode coordinates, multiple-match failure, `--first` | C: ClickUnicodeCoordinates, MouseAndFrameCapture, pattern regressions | verified |
| Click-at uses valid zero-based terminal coordinates; SKILL:175 | commands_input.go, session/ClickAt | Negative coordinates are rejected before PTY input | B: `(0,0)` SGR events emitted; IA-009 | violated |
| Scroll validates direction/lines/coordinates and emits wheel events; SKILL | app/scrollOptions, session/scroll | Negative counts/coords have no input side effect; zero lines check lifecycle | C: input regression/validation tests | verified |
| Resize honors native PTY bounds and failure consistency; SKILL/AGENTS repair scope | session/Resize, validateDimensions | Invalid sizes fail before emulator changes; real SIGWINCH/PTY size agrees | C: dimensions/input tests, AttachTerminalOutputAndResize | verified |
| Frame capture validates before input, count and intervals, closed-session rejection; SKILL | CaptureFrames, commands_input.go | Invalid count/interval/key sends no bytes; close interrupts delay | C: input validation and frame tests | verified |
| Screenshot renders fonts/styles/colors/geometry, validates raster capacity; SKILL | screenshot package | Decode actual PNGs; check glyph/style pixels, limits and overflow | C: renderer/geometry tests | verified |
| Explicit relative screenshot paths resolve against caller cwd; SKILL | commands_screenshot.go | Actual file appears beneath caller cwd, not daemon HOME | C: screenshot paths, ReviewRelativeScreenshot | verified |
| Default screenshot output remains request-specific | commands_screenshot.go | Concurrent successful captures have distinct retained files | P: 20 requests, 6 paths; IA-006 | violated |
| Close removes session and joins process-group escalation; SKILL | registry.Close, CloseAndWait | Signal-resistant child eventually killed; closing notifications don't deadlock | C: shutdown/ownership tests; P: close control | verified |
| Daemon-stop terminates associated children, retains port until joined cleanup; SKILL:254-256 | main/runRelayServer, registry.Shutdown | Children gone before port released; pending launches rejected | C: ReviewDaemonShutdown, ownership tests | verified |
| Daemon-stop affects the daemon and associated processes; SKILL:254 | client/KillRelay, platform port enumeration | Separate listener on another address survives | B: unrelated listener receives SIGTERM; IA-001 | violated |
| Attach replays raw/binary output, forwards input/resize, restores terminal; SKILL/README | app/attach.go, relay/handleAttach | Real PTY and WebSocket tests verify output and cancellation | C: attach migration/binary tests | verified |
| Double Ctrl+X kills process and detaches; SKILL:261 | attachInput.forward, handleAttach kill | Kill control terminates a signal-resistant process | P: child remains alive after 2.5 s; IA-004 | violated |
| Agent output is selected per request; README/SKILL | agent package, commandContext | Mixed callers to one daemon get correct compact/human formats | C: ReviewPerRequestAgentOutput, caller output tests | verified |
| Embedded skill is verbatim, offline, no positional args, propagates write failures; SKILL | cmd/skill.go | Both output modes, binary outside repo, extra args and failing writer | C: SkillOutputContract and compiled offline tests | verified |
| Every help path starts with alert in agent mode; SKILL | app/setupHelp | Root, every registered subcommand, generated help/completion and skill paths | C: offline help tests; metadata probe: all 27 paths | verified |
| Operational guide covers full live interface; applicable CLI skill | both SKILL.md files, help/flag metadata | Generated commands and metadata appear in guide; drift fails tests | S/B: commands omitted, no exhaustive comparison; IA-011 | violated |
| Maintained skill copies agree; AGENTS | cmd/tuistory/SKILL.md, skills/tuistory/SKILL.md | Byte comparison succeeds | `cmp` exit 0 | verified |
| No-argument commands reject unexpected positional data | CLI declarations and SKILL command signatures | Unexpected argument cannot execute close | B: extra-argument close succeeds and removes session; IA-012 | violated |
| Loopback/browser request restrictions; README | CheckLocalOnly, listener setup | Origin and hostile Host/fetch markers denied | C: relay middleware tests; P: Origin 403 | verified |
| Isolation from untrusted local callers | HTTP/WS API, client transport | Unauthorized local caller cannot launch/read/kill | Trust boundary unspecified; credential-free execution confirmed; Q-001 | blocked |
| Human/agent launch modes and passthrough child status; SKILL | cmd/client.go, attach.go | Foreground passthrough returns actual child status; agent launch is background | C: ClientOfflineAndPassthrough, E2E_NestedPassthrough | verified |
| Logs, PID/restart-lock recovery, semantic daemon version negotiation; SKILL/README | client.go, main.go, relay paths | Probe rejects malformed/missing protocol; lock recovery and log access | C: client behavior/protocol/recovery, E2E_Logfile | verified |
| README published installation example names an existing release | README, release assets | Resolve published tag/archive and execute extracted binary | Tag v0.0.1 verified online; assets/install execution not performed | unverified |

## Prioritized findings

All twelve findings below have high confidence. They are new IDs for this report; no prior report existed at the default path. Severity follows the invoked audit skill: high for broken mandatory behavior or substantial reliability risk, medium for bounded defects or concrete maintenance hazards. No production fixes were applied.

### IA-001 — High: daemon-stop kills unrelated same-port listeners

**Contract:** SKILL.md:254 says daemon-stop terminates the relay and its associated sessions/children. **Locations:** [client.go](../../../internal/client/client.go):115-164 (`KillRelay`, `killProcessOnPort`), [port_linux.go](../../../internal/client/port_linux.go):13-69, [port_darwin.go](../../../internal/client/port_darwin.go):103-136; caller [cmd/client.go](../../../cmd/tuistory/client.go):98-113.

**Trigger/result:** Own daemon bound to `127.0.0.1:32913`; separate Bun child bound successfully to `127.0.0.2:32913`. Running daemon-stop returned exit 0 and `Daemon stopped`. The unrelated listener's `signalCode` became `SIGTERM`. Both fixtures were owned by this audit. This reproduces unintended termination, not hypothetical PID-file reuse.

**Root cause/impact:** Enumeration matches the numeric port across all listening addresses, then signals every owner. The Linux implementation discards the address in `/proc/net/tcp*`; the Darwin implementation likewise compares port without address. The client can terminate independent local services and their active work. Linux is reproduced; Darwin's analogous source path is inspected but not executed.

**Correction/regression:** Identify ownership of the actual relay endpoint, preserve full address/family in socket matching, and establish which process is safe to replace. Test two real processes listening on distinct addresses at the same port; the unrelated owner must survive daemon-stop and automatic recovery. Retain stale-PID and joined-shutdown tests.

### IA-002 — High: restart replaces the original executable/argv with a shell

**Contract:** SKILL.md:74 and :234 promise identical original arguments, cwd, dimensions, and environment on restart. **Locations:** [commands_lifecycle.go](../../../internal/app/commands_lifecycle.go):109-138 (`restartOptions.prepare`), [session.go](../../../internal/session/session.go):210-220 (stores command label), [launch.go](../../../internal/app/launch.go):10-21.

**Trigger/result:** An executable helper in the sole PATH directory launches successfully and prints its literal argument `one two;$HOME`. Restart under that same valid PATH fails with `resolving command: exec: "sh": executable file not found in $PATH`. The session list becomes `[]`. The fixture helper itself uses an absolute interpreter path and therefore does not require `sh` in PATH.

**Root cause/impact:** Session metadata retains a presentation label; restart reinterprets it using `sh -c` rather than replaying the structured launch. This adds a runtime shell requirement to direct execution and removes the old session before replacement can start. Shell command interpretation can also differ from direct executable selection; those additional cases were not independently executed.

**Correction/regression:** Retain the structured original launch specification and restart through the same direct-execution path. Preserve explicit shell launches as such. Verify argv containing empty strings, quotes/metacharacters, shell-reserved executable names, restricted/relative PATH, cwd, environment, and dimensions against the child itself. Test replacement start failure and document its session-state outcome.

### IA-003 — High: foreground passthrough ignores the executable PATH override

**Contract:** SKILL.md:76 states executable names use caller PATH, including `--env PATH=...`; :80 covers foreground passthrough with requested cwd/environment. **Location:** [cmd/client.go](../../../cmd/tuistory/client.go):121-139 (`runPassthrough`).

**Trigger/result:** Set `TUISTORY_SESSION=owned-audit`, then invoke `--env PATH=<helper-directory> -- audit-helper nested`. Exit 1: `spawning command: exec: "audit-helper": executable file not found in $PATH`. The same arguments with the helper's absolute path succeed and print `HELPER:nested`; daemon-mode PATH launch also succeeds.

**Root cause/impact:** `exec.Command(program, ...)` resolves a bare name before `child.Env` and `child.Dir` are assigned. Verified in installed Go 1.26.8 `os/exec/exec.go` and [official exec.Command documentation](https://pkg.go.dev/os/exec#Command). Nested launch, TRAFORO_URL, and SIGILLO select this path; only the nested selector was executed in the reproduction. Commands can fail or choose a different executable from the requested environment.

**Correction/regression:** Resolve the executable against effective cwd/env before constructing the child, consistently with daemon launch, using the existing resolution behavior rather than a second divergent policy. Test an override-only helper and a relative PATH entry under a different requested cwd in each passthrough selector, with an absolute-path control.

### IA-004 — High: attach's kill action never escalates

**Contract:** SKILL.md:261 documents double Ctrl+X as “kill process and detach.” **Locations:** [attach.go](../../../internal/app/attach.go) (`attachInput.forward`), [server.go](../../../internal/relay/server.go):270-273, [lifecycle.go](../../../internal/session/lifecycle.go):82-90 (`KillProcess`).

**Trigger/result:** Launch `/bin/sh -c 'trap ... TERM HUP; ...; exec sleep 60'`, attach a real WebSocket, and send its `kill` control. After 2.5 seconds, `sessions --json` still reports `dead:false`. A subsequent ordinary close succeeds after ~2 seconds, demonstrating working escalation through the close path.

**Root cause/impact:** The attach control calls a one-shot SIGTERM method and does not run joined termination or SIGKILL escalation. The client detaches regardless of actual process exit. The runtime reproduction covers the exact server control sent by double Ctrl+X; keyboard detection is independently covered by `TestAttachControlKeys`.

**Correction/regression:** Make kill use owned process-group termination with escalation and explicit completion/error semantics. Preserve intended retention of exited session output if that is part of the chosen contract. Test the actual attach action with TERM-resistant root/descendants and assert termination, not just that a kill message was sent.

### IA-005 — High: idle timeout is reported as successful stabilization

**Contract:** SKILL.md:135-142 says wait until no PTY output for ~200 ms, with a maximum wait duration; non-immediate snapshot and screenshot also depend on idle behavior. **Locations:** [output.go](../../../internal/session/output.go):76-97, :119-131; [commands_read.go](../../../internal/app/commands_read.go):175-196; [commands_screenshot.go](../../../internal/app/commands_screenshot.go):49.

**Trigger/result:** Child writes `tick` every 10 ms. `wait-idle --timeout 50` returns exit 0 and `OK` after 55 ms; the child remains active and subsequent read contains continuing ticks. Fifty milliseconds cannot establish the documented 200 ms quiet interval.

**Root cause/impact:** `WaitIdle` returns nil both when the timer fires and when the deadline expires. The command discards its return value anyway. `Text` calls it with 15 ms and ignores the result; screenshot also ignores it. Callers cannot distinguish synchronization success from a deadline and may act on intermediate output.

**Test evidence:** `TestWaiterTimeoutCleanup` explicitly requires a 1 ms idle wait to return nil; this verifies waiter disposal but locks in incorrect result semantics. `TestAlreadyIdle` is useful and should remain.

**Correction/regression:** Return a distinct timeout/cancellation failure and propagate it where idle is promised. Define how snapshot's overall timeout constrains idle waiting. Test continuous output, output stopping before deadline, already-idle operation, and closure; assert elapsed quiet time and exit status. Replace the current timeout-success expectation with a requirement-derived assertion.

### IA-006 — Medium: concurrent default screenshots reuse and overwrite files

**Contract:** Successful screenshot returns the file containing that capture; independent requests must not overwrite another request's default artifact. **Location:** [commands_screenshot.go](../../../internal/app/commands_screenshot.go):55-68.

**Trigger/result:** Twenty concurrent `screenshot --immediate --padding 0` commands against a 2x1 terminal all succeed but return only six distinct paths. These paths live under the audit TMPDIR. Source writes every result using `os.WriteFile` at a name containing only Unix milliseconds, so repeated names target the same file.

**Root cause/impact:** Timestamp names provide no exclusive file creation. Concurrency causes artifact identity loss and overlapping writes. The reproduction uses identical content; corruption of a PNG with different content was not separately measured. Explicit user-requested overwrite behavior is outside this finding.

**Correction/regression:** Create unique default files with native exclusive temporary-file creation and safe cleanup on write failure. Test concurrent captures from differently colored sessions; assert unique paths and decode each expected image. Consider default artifact permissions together with the local trust model.

### IA-007 — Medium: advertised navigation key chords lose modifiers

**Contract:** SKILL.md:148-162 advertises Ctrl/Alt/Shift/Meta modifiers and navigation/function keys. **Location:** [keys.go](../../../internal/keys/keys.go):111-195 (`GetKeyCode`).

**Trigger/result:** Real raw-mode child reads input using `dd` and prints hex with `od`. `press ctrl left` receives `1b 5b 44` (ordinary left). `press shift up` receives `1b 5b 41` (ordinary up). Both CLI commands return `OK`.

**Root cause/impact:** Special keys outside the small CSI-u table use unmodified `keyCodes`; Ctrl/Shift are discarded. Meta is accepted and explicitly ignored. Some ordinary combined Ctrl+Alt chords also skip Alt encoding by branch structure; those were not executed here. Navigation/selection shortcuts therefore become different actions. [Xterm's maintained control-sequence reference](https://invisible-island.net/xterm/ctlseqs/ctlseqs.html#h3-PC-Style-Function-Keys) specifies modifier parameters for cursor/editing/function keys; the reproduced modifier loss is independent of Meta's configurable behavior.

**Correction/regression:** Implement the advertised supported terminal key encoding consistently, including modifiers and applicable terminal modes. Test actual child bytes against independently documented sequences, including Ctrl-left and Shift-up, and test a consuming TUI. `TestModifierEncoding` currently expects Meta-a to equal a and Alt-up to be a prefixed ordinary arrow; such expectations need explicit protocol justification rather than copying branches.

### IA-008 — Medium: wait removes actual block glyphs from match text

**Contract:** SKILL.md:133 says string patterns match literally. **Location:** [output.go](../../../internal/session/output.go):122-130.

**Trigger/result:** Live child prints `BLOCK:█` and sleeps. Immediate snapshot shows the printed block and the added cursor marker. `wait '█' --timeout 100` exits 1, while the timeout diagnostic itself displays `BLOCK:█`.

**Root cause/impact:** `waitText` is already rendered with `ShowCursor=false`, yet `strings.ReplaceAll(waitText, CursorChar, "")` deletes genuine terminal content. Plain/regex patterns containing that glyph cannot match, and removal can also create matches across content that was actually separated by blocks.

**Correction/regression:** Match the cursor-free rendered content without deleting real glyphs. Test literal and regex patterns containing blocks, block-only output, and patterns that must not match across a block. Assert readiness against real terminal output.

### IA-009 — Medium: click-at permits negative and overflowing coordinates

**Contract:** SKILL.md:175-176 and help specify zero-based terminal cell coordinates. **Locations:** [commands_input.go](../../../internal/app/commands_input.go):93-119, [input.go](../../../internal/session/input.go):97-119.

**Trigger/result:** `click-at -- -1 -1` exits 0 and `OK`. A raw-mode child receives both `ESC[<0;0;0M` and `ESC[<0;0;0m`, verified as 18 hexadecimal bytes. Negative zero-based input has been translated into invalid zero terminal ordinates. The command parses integers but does not validate sign or safely bound the `+1` conversion. MaxInt overflow is source-confirmed, not executed.

**Root cause/impact:** Missing shared coordinate validation allows invalid mouse events through a public trust boundary. Scroll validates negative coordinates at the CLI level while click-at does not, creating divergent input contracts.

**Correction/regression:** Validate at the session boundary before any write, covering nonnegative inputs and safe one-based conversion. Decide and document whether coordinates outside current dimensions are rejected; no undocumented screen-boundary policy is imposed by this audit. Use a real raw child to assert no bytes for rejected values and correct first/last accepted coordinates.

### IA-010 — Medium: incremental read strips ANSI per slice instead of per stream

**Contract:** SKILL.md:114-120 and README promise an ANSI-free incremental output stream. **Location:** [output.go](../../../internal/session/output.go):15-22, :271-302.

**Trigger/result:** A child disables echo, writes the first bytes `ESC[31`, waits for input, then writes `mRED ESC[0m`. First incremental read returns the raw `ESC[31`; after pressing Enter, the second returns `mRED`. `read --all` returns the correct `RED`, proving the complete buffered escape is recognized but incremental slices are mishandled.

**Root cause/impact:** `Read` advances its cursor and applies a stateless complete-sequence regex only to the newly selected bytes. A control sequence split at a read boundary is irrecoverably leaked or partially displayed, defeating the promised plain-text stream and potentially altering the caller's terminal.

**Correction/regression:** Preserve escape-parser state across incremental reads while keeping the original raw buffer for attach, using maintained parsing facilities where suitable. Test all split points of CSI/OSC/DCS sequences, long fragments, retention truncation, and cursor behavior; compare concatenated incremental text with independently expected plain output.

### IA-011 — Medium: operational reference and its tests omit the full live interface

**Rule:** Applicable CLI skill, “Embedded Skill Contract,” requires all public and generated help/completion commands and comparison with live command/flag metadata. AGENTS requires command-tree unit/E2E coverage and synchronized guides. **Locations:** [both guides](../../../cmd/tuistory/SKILL.md), [help.go](../../../internal/app/help.go):106-140, [main_test.go](../../../cmd/tuistory/main_test.go), [e2e_test.go](../../../cmd/tuistory/e2e_test.go):160-168.

**Observed mismatch:** `completion bash --help` is executable and correctly displays the agent alert. The operational guides have no reference for generated `help`, `completion`, or its shell subcommands/flags. The parser's help/version short aliases are not fully catalogued. Existing tests cover selected help screens and embedded-byte equality, but none compares every command/flag against the operational guide. Byte identity of the two copies is verified and does not address this omission.

**Impact/correction:** Agents instructed to use the guide as a complete operational reference cannot discover these supported commands from it; future interface drift can pass the suite. Extend both copies together and add a behavior-based live-tree/flag-metadata coverage check that fails when a real exposed option is undocumented. Keep successful offline/verbatim/write-failure/help-alert tests. This is an explicit policy violation, not a preference for more prose.

### IA-012 — Medium: no-argument commands silently accept extra positional input

**Contract:** Guide command signatures document `close`, `restart`, `sessions`, `snapshot`, `read`, `wait-idle`, `screenshot`, `logfile`, `daemon-stop`, and `attach` without positional arguments. **Locations:** commands_lifecycle.go:15-108; commands_read.go:26,94,181; commands_screenshot.go:22; commands_metadata.go:12,24,37.

**Trigger/result:** `sessions unexpected-argument --json` succeeds. More consequentially, `-s block close unexpected` returns exit 0 and closes the session; the next snapshot fails `Session "block" not found`. These commands lack `Args: cobra.NoArgs`, unlike the correctly guarded skill command and commands using ExactArgs.

**Impact/correction:** Mistyped or misunderstood requests look successful, and a destructive action runs despite an invalid signature. Add framework arity validation consistently before side effects. Test extra arguments for every no-positional command and verify that close/restart/daemon-stop leave owned live fixtures unchanged when validation fails. Public behavior corrections require the applicable user authorization before implementation.

## Audit coverage ledger and inspection inventory

The complete tracked-file inventory is saved at `.tmp/implementation-audit/tracked-files.txt`; all 44 test files were inspected, not only executed. Production paths are accounted for below.

| Surface | Files inspected and execution paths traced | Results / limits |
| --- | --- | --- |
| CLI entrypoints | cmd/tuistory/main.go, root.go, client.go, skill.go; embedded SKILL.md | Client/daemon dispatch, help/version/skill offline paths, forwarding, TTY attach, passthrough, shutdown; IA-003,011,012 |
| Application command tree | internal/app/cli.go, help.go, launch.go, commands_launch.go, commands_lifecycle.go, commands_metadata.go, commands_input.go, commands_read.go, commands_screenshot.go, format.go, attach.go | All declared commands and flags traced to session/relay/native child paths; IA-002,004,005,006,009,011,012 |
| Client lifecycle / transport | internal/client/client.go, port_linux.go, port_darwin.go | HTTP probe, semver/protocol, startup/restart lock, forwarding, listener ownership; IA-001; Darwin source/cross-build only |
| Daemon registry / endpoints | internal/relay/server.go, registry.go | /version, /sessions, /cli, /attach; security middleware, per-name reservations, replacement, shutdown, notification lifecycles; Q-001 |
| Session and PTY | internal/session/session.go, lifecycle.go, shutdown.go, output.go, input.go, numeric.go, pattern.go, process_io.go, process_resolution.go, terminal_response.go | Native PTY setup, process groups, output drain, bounded retention, callbacks/backpressure, replies, waits, input, snapshots; IA-002,004,005,008,009,010 |
| OS process discovery | internal/process/groups.go, pids_linux.go, pids_darwin.go | Session/group ownership, TERM/KILL, Linux proc and Darwin native discovery; race/process tests; native Darwin pending |
| Key encoding / mode detection | internal/keys/keys.go; internal/agent/agent.go | Accepted key vocabulary/chords and per-request agent truthiness; IA-007 |
| PNG rendering | internal/screenshot/screenshot.go, render.go, geometry.go, font.go; fonts/* | Real parse, raster/color/geometry/font fallback, overflow preflight, embedded licenses; IA-006; complete visual fidelity not asserted |
| Repository and delivery | AGENTS.md, README.md, skills/tuistory/SKILL.md, go.mod/go.sum, LICENSE, .gitignore, opencode.json, justfile, scripts/coverage.sh, .goreleaser.yml, three workflow files | Toolchain/deps, docs, automation, fresh coverage, native target compilation, release gating; no external_directory interaction from opencode config |

| Audit category | Checks and findings | Coverage limits / exclusions |
| --- | --- | --- |
| Behavior/completeness | Full command paths, argument/env/cwd precedence, validation, Unicode/regex, native input, CLI results; IA-002 through IA-012 | Exact additional timeout/coordinate acceptance rules remain unspecified |
| Architecture/semantic integrity | Cobra/help-tree use, session/registry ownership, pollable PTY primitive, separate reply writer, dependency direction; divergent executable resolution/restart representation identified | No rewrite proposed on file length alone; no reachable production stubs established |
| Security/privacy | Origin/Host/fetch checks, native launch versus explicit shell, WebSocket frame handling, local command execution; Q-001 | No cross-user exploit, symlink attack, dependency vulnerability scan, or remote attack performed |
| Data integrity/persistence | In-memory registry reservations and retention; screenshot file identity; PID/restart-lock recovery; IA-006 | No application DB or durable session recovery contract; optional .termcast-bundle cleanup source/tests inspected, integration with its originating tool not executed |
| Concurrency/lifecycle/reliability | Fresh race tests; pending launch/restart shutdown, process-group escalation, output/subscription ordering, slow readers, terminal replies; IA-001,004,005,006 | Compiled E2E daemon lacks -race; additional native stress/fault injection not performed |
| Performance/capacity | Output and subscriber/reply bounds, renderer preflight, actual concurrent screenshot workload | No throughput/latency target or representative large-workload benchmark; terminal aggregate allocation and request-body limits remain hypotheses |
| Interfaces/UX | Human/agent modes, tree help, offline metadata, errors/status, raw PTY interactions, built-in completion help; IA-003,005,007,009,011,012 | No manual native macOS terminal session; real PTY attach is covered by Linux tests |
| Configuration/packaging/operations | Go1.26.8, just, env paths, semver/protocol checks, four cross-builds, GoReleaser check, release workflow graph and published tag | Actual archives/install/checksums and current hosted CI results not executed; no publish permission implied |
| Test quality | All 44 test files read, fresh execution, raw-child adversarial probes, contract-vs-expectation review; IA-005,007,011 | No tracked-file mutation experiment; passing percentage does not prove acceptance |
| Model/tool integration | Embedded operational guide, help-alert enforcement and public CLI semantics inspected | No embedded model service or tool-permission dispatcher; prompt injection categories otherwise not applicable |

## Execution evidence

All commands ran from the repository root. Logs below are project-local ignored artifacts; this report preserves the essential results even if scratch files are later cleaned. No coverage from another source revision was combined.

| Command / environment | Exit status | Evidence |
| --- | --- | --- |
| `go version`; `just --version`; `bun --version` | 0 | go1.26.8 linux/amd64; just1.58.0; Bun1.4.0 |
| `just check` | 0 | [.tmp/implementation-audit/just-check.log](../../../.tmp/implementation-audit/just-check.log): go vet; fresh `go test -race -v -count=1 -covermode=atomic -coverpkg=./... ./...`; merged native coverage |
| `go mod tidy -diff` | 0 | [tidy.log](../../../.tmp/implementation-audit/tidy.log), empty/no changes |
| `go mod verify` | 0 | [modules.log](../../../.tmp/implementation-audit/modules.log): `all modules verified` |
| `just build` | 0 | [just-build.log](../../../.tmp/implementation-audit/just-build.log), bin/tuistory |
| `go build -o bin/tuistory-audit ./cmd/tuistory` | 0 | [build.log](../../../.tmp/implementation-audit/build.log); real binary used in probes |
| `GOOS=darwin GOARCH=arm64 go build -o bin/tuistory-audit-darwin-arm64 ./cmd/tuistory` | 0 | [darwin-arm64-build.log](../../../.tmp/implementation-audit/darwin-arm64-build.log) |
| `GOOS=darwin GOARCH=amd64 go build -o bin/tuistory-audit-darwin-amd64 ./cmd/tuistory` | 0 | [darwin-amd64-build.log](../../../.tmp/implementation-audit/darwin-amd64-build.log) |
| `GOOS=linux GOARCH=arm64 go build -o bin/tuistory-audit-linux-arm64 ./cmd/tuistory` | 0 | [linux-arm64-build.log](../../../.tmp/implementation-audit/linux-arm64-build.log) |
| `goreleaser check` | 0 | [goreleaser-check.log](../../../.tmp/implementation-audit/goreleaser-check.log): one configuration validated, installed v2.15.4 |
| `cmp cmd/tuistory/SKILL.md skills/tuistory/SKILL.md` | 0 | Byte-identical guides |
| `bun .tmp/implementation-audit/probe.ts` | 0 | [probe.ts](../../../.tmp/implementation-audit/probe.ts), [probe.log](../../../.tmp/implementation-audit/probe.log), [probe-results.json](../../../.tmp/implementation-audit/probe-results.json) |
| `bun .tmp/implementation-audit/boundaries.ts` | 0 | [boundaries.ts](../../../.tmp/implementation-audit/boundaries.ts), [boundaries.log](../../../.tmp/implementation-audit/boundaries.log), owned neighbor.ts helper |
| `bun .tmp/implementation-audit/metadata.ts` | 0 | [metadata.ts](../../../.tmp/implementation-audit/metadata.ts), [metadata.log](../../../.tmp/implementation-audit/metadata.log): 27 help paths return exit 0 with the agent alert; existing report links resolve |
| `go tool cover -func=.tmp/coverage.out` | 0 | [coverage-functions.txt](../../../.tmp/implementation-audit/coverage-functions.txt), report snapshot [coverage.out](../../../.tmp/implementation-audit/coverage.out) |

Probe harness exit 0 means the experiment and cleanup completed; it does **not** mean its acceptance conditions passed. Each subprocess's args, exit status, stdout, stderr, and elapsed time are retained as JSON lines. Defect probes intentionally observe failures and incorrect success responses without modifying production code.

### Measured test and coverage results

All nine package test runs passed under the fresh required check. The coverage script combined fresh `.tmp/coverage-unit/` with `.tmp/e2e-coverage/` into `.tmp/coverage-merged/` and generated `.tmp/coverage.out`. The compiled E2E coverage directory is cleared by TestMain for this run. Final output:

```text
total: (statements) 92.5%
Behavioral statement coverage: 92.50% (minimum 90%)
```

Representative real boundaries covered by the suite include initial PTY dimensions, caller PATH/cwd, terminal query replies, scroll/resize races, output-before-exit drain, process-group orphan cleanup, slow-subscriber cancellation, joined registry shutdown, binary WebSocket output, embedded guide offline execution, and per-caller agent formatting. Independent metadata verification additionally exercised all 27 public help paths, including generated shell completion commands; each returned exit 0 and the required leading alert. These are substantive checks. The audit found uncovered combinations and some incorrect expected results.

`cmd/tuistory/e2e_test.go:77` builds its executed daemon with `go build -cover ...`, without `-race`. The enclosing Go test processes have race instrumentation, but the separately built E2E CLI/daemon does not. The statement percentage also does not measure conditional-contract correctness. Existing green checks coexist with the twelve reproduced findings.

### Sensitivity and controls

No tracked implementation was disabled or mutated: the invoked audit authorizes isolated probes, but requires separate authorization for experiments altering tracked files. The requested red/green repair workflow therefore was not performed in this audit. No claim is made that the existing suite would detect a targeted implementation fault.

Independent controls demonstrated failure sensitivity without source edits: daemon helper launch and absolute passthrough succeed while PATH-only passthrough fails; ordinary close escalates while attach kill does not; snapshot/timeout diagnostic contains the block while literal wait fails; full ANSI read is correct while incremental reads leak fragments. The unrelated listener was demonstrably listening on the distinct address before shutdown. Real child byte capture verifies input side effects rather than comparing encoder output to itself.

## Open questions, limitations, and separate observations

### Q-001 — Local daemon trust boundary is unspecified

The user was asked whether access should be restricted to the current OS user or all local processes should be trusted. No answer was available when this report was written. The isolation requirement is **blocked**, not presumed satisfied or violated.

Verified behavior: a POST to `/cli` with an Origin header returns 403. Removing that header and supplying only Content-Type allows an otherwise unauthenticated launch of `/bin/sh -c 'id -u'`; read returns uid1000, the daemon owner's UID. `CheckLocalOnly` inspects browser-origin, Host, and fetch headers; no caller credential/UID is established before /cli, /sessions, or /attach. The daemon binds TCP loopback. This is confirmed credential-free local command execution and a material security exposure if untrusted local callers are in scope. Cross-user exploitation was not executed and is not represented as verified.

Next check: decide the intended trust model, then test two actual OS identities against the real transport. If isolation is required, design and verify an owner-restricted transport or authenticated requests for every data/action endpoint, and reconsider predictable shared temp files/logs. This audit does not silently impose an authentication architecture.

### Remaining verification limits

- **Native platforms:** Darwin process/socket ABI, race behavior, PTY cleanup, and attach must be executed on macOS using the pinned toolchain. Linux/arm64 also requires execution. Successful cross-builds do not prove OS semantics.
- **Published artifact/install path:** The [latest release page](https://github.com/alexgorbatchev/tuistory/releases/tag/v0.0.1) resolves to v0.0.1 and commit `6aeed8d`, matching the local tag, rather than the audited revision. The README explicitly names that release. Archive bytes, checksums, extraction/install, embedded license accessibility, and runtime behavior were not validated here; execute a disposable install and compare its behavior before claiming the current repairs are distributed. No release was published.
- **Current hosted CI:** Workflow files define Linux/macOS gates and go.mod selection; this audit did not inspect the current revision's remote run result. The configured v7 action tags were checked against their maintainers' release pages: [checkout](https://github.com/actions/checkout/releases/tag/v7), [setup-go](https://github.com/actions/setup-go/releases/tag/v7), [GoReleaser action](https://github.com/goreleaser/goreleaser-action/releases/tag/v7).
- **Capacity hypotheses:** Per-axis terminal bounds still permit a very large aggregate cell count; HTTP JSON bodies have no application body cap/read deadlines. No destructive allocation or load test was run. Use an isolated resource-limited process to measure representative and adversarial sizes before setting evidence-based limits. Renderer raster preflight is separately exercised and passes.
- **Terminal fidelity:** Xterm application-cursor/key negotiation, modified special-key interoperability, and all glyph/geometry combinations were not exhaustively tested. The child-byte defects in IA-007 are established; complete emulator equivalence is not claimed.
- **Dependency/security analysis:** Version-pinned source/API checks and `go mod verify` establish usage and checksum integrity, not absence of vulnerabilities. No dependency vulnerability scanner, full dependency code audit, or malicious supply-chain simulation was executed.
- **Migration policy checklist:** This audit does not update the ignored migration repair checklist or mark new repairs landed. No fixes landed. Reconcile these findings into that checklist if a repair is explicitly requested.

Official external semantics were checked where needed: [Go exec.Command](https://pkg.go.dev/os/exec#Command), [pinned PTY API](https://pkg.go.dev/github.com/creack/pty@v1.1.24#Setsize), [pinned semantic-version API](https://pkg.go.dev/golang.org/x/mod@v0.41.0/semver), [pinned WebSocket close semantics](https://pkg.go.dev/github.com/coder/websocket@v1.8.15#Conn.Close), [xterm protocol reference](https://invisible-island.net/xterm/ctlseqs/ctlseqs.html), and [GoReleaser configuration checking](https://www.goreleaser.com/customization/). Installed Go/WebSocket source was inspected where online documentation might describe a different version. Bun's maintained process/TCP/file APIs were checked for the scratch harness.

## Remediation order

1. Resolve Q-001's trust boundary; if untrusted local callers are supported, establish a verified security boundary before release acceptance. Fix IA-001's unrelated-process termination first. These changes need real ownership/isolation tests.
2. Fix IA-002 and IA-003 around the structured launch specification and one effective executable-resolution contract. These are related architectural paths; avoid rebuilding restart from a display string.
3. Fix IA-004 joined kill semantics and IA-005 timeout/idle propagation, keeping existing ownership, escalation, and callback-order tests.
4. Fix IA-006 unique screenshot artifacts, IA-007 protocol-correct key chords, IA-008 literal matching, IA-009 mouse validation, and IA-010 incremental ANSI parsing with real child/file side-effect assertions.
5. Fix IA-012 arity validation and IA-011 complete live-interface documentation/testing in the same authorized public CLI correction workflow. Keep the two guides synchronized for every affected behavior.
6. Follow the project's red/green and change-disabled sensitivity checks for the repairs, then run fresh `just check`, `go mod tidy -diff`, native macOS verification, and isolated artifact/install checks. Do not merge old coverage with repaired revisions. Publishing still requires explicit authorization.

This audit accounts for the selected repository and records evidence and limitations. Acceptance is withheld because documented mandatory behavior is demonstrably broken; passing automation alone does not resolve those failures.

## GitHub issue tracking

GitHub Issues is enabled for `alexgorbatchev/tuistory` at the user's request. All findings and due-diligence items are filed and remotely verified: IA-001 through IA-012 map to #1 through #12; Q-001 maps to #13; native/hosted verification, artifacts/install, terminal capacity, HTTP capacity, dependency assessment, compiled E2E race coverage, terminal fidelity, and repair-test sensitivity map to #14 through #21. Issue #22 tracks the complete repair and verification checklist.

The [issue index](implementation-audit-issues.md) records every issue title, classification, label, and relationship. Filing does not change the audit verdict or mark any repair landed. Fresh required checks for the issue-filing work pass with 92.46% coverage and no module-hygiene diff, recorded separately from the audit's original execution evidence.
