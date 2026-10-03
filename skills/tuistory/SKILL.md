---
name: tuistory
description: Use when launching, inspecting, waiting on, and interacting with background terminal sessions, dev servers, CLIs, or TUIs.
author: alexgorbatchev
metadata:
  created_on: 2026-04-14 12:00
  last_modified: 2026-10-03 11:40
  status: current
---

Run dev servers and terminal commands in named background sessions that agents can inspect, wait on, and type into.

## Core Rules

1. **Options before `--`, command after**: Anything after `--` is passed verbatim to the child command.
   ```bash
   # Correct
   tuistory -s myserver --cols 150 -- ./server --port 3000
   tuistory -- ./server

   # Wrong: options after -- are passed to node, not tuistory
   tuistory -- node server.js -s myserver
   ```
2. **Auto-derived session names**: When `-s` is omitted, the session name defaults to `<cwd-basename>-<cwd-hash>-<command>` in kebab-case.
3. **Inspect after every action**: Run `tuistory -s <name> snapshot --trim` after typing or pressing keys to observe the new terminal state.
4. **Reactive waiting instead of sleep**: Never use blind `sleep`. Use `wait` (string or regex) or `wait-idle` to react immediately as output settles.
5. **Shared dev servers**: Re-running `tuistory -- <cmd>` while a session is already alive returns immediately with status and commands rather than failing with port conflicts.
6. **Do not close unowned sessions**: Sessions running shared dev servers must remain alive unless explicitly requested to shut down.

## Operational Workflow

### Background Dev Server (replaces tmux)

```bash
# 1. Start background session
tuistory -- ./server

# 2. Wait reactively for server readiness (case-insensitive regex)
tuistory -s <name> wait "/ready|listening/i" --timeout 30000

# 3. Read output log
tuistory read -s <name>

# 4. Restart server after code change (sends Ctrl+C, waits, relaunches same cmd/cwd/env)
tuistory -s <name> restart

# 5. Stop server when requested
tuistory -s <name> close
```

### Interactive TUI Loop (observe -> act -> observe)

```bash
# Observe initial screen
tuistory -s app snapshot --trim

# Type input or send key chords
tuistory -s app type "search query"
tuistory -s app press enter

# Observe updated screen
tuistory -s app snapshot --trim

# Click on rendered button or link
tuistory -s app click "Submit" --first
tuistory -s app snapshot --trim
```

## Command Reference

### `tuistory [options] -- <command>` / `tuistory launch [command]`
Launch a terminal session in the background daemon with a PTY.

Use `--` to preserve each argument literally, including spaces, dollar signs, and shell metacharacters. Use an explicit shell when shell syntax is needed: `tuistory -- sh -c 'printf ready; exec ./server'`. The positional `launch "command"` form accepts shell source. Restart preserves the original arguments, cwd, dimensions, and environment.

Executable names are resolved using the caller's `PATH`, including overrides supplied with `--env PATH=...`. Relative PATH entries are relative to the child working directory. Concurrent launches for the same session name reuse one live process. Launch and restart reject new sessions while the daemon is shutting down.

PTY dimensions accept columns from 2 through 65535 and rows from 1 through 65535. Launch values of zero select the defaults, 120 columns and 36 rows. Negative or out-of-range dimensions return an error before a child is started.

In a terminal, launch attaches automatically unless `--background` or agent mode is enabled. Inside an existing tuistory session, or when `TRAFORO_URL` or `SIGILLO` is set, launch runs the child in the foreground with the requested cwd and environment.

Without `--no-wait`, launch waits for initial PTY output. A live silent process remains running and produces a timeout warning; a process that exits without output produces an error and remains available for inspection.

A nonpositive launch `--timeout` selects the 5000-millisecond startup wait. Silent-process warnings report the effective wait duration.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | auto | Session name (`<cwd-basename>-<hash>-<command>`) |
| `--cols` | | `120` | Terminal columns |
| `--rows` | | `36` | Terminal rows |
| `--cwd` | | caller cwd | Working directory for child process |
| `--env` | | `[]` | Environment variable `KEY=VAL` (repeatable; commas remain part of the value) |
| `--attach` | | `false` | Deprecated accepted flag; attachment is automatic in terminal mode |
| `--background` | | `false` | Run in background without attaching |
| `--no-wait` | | `false` | Skip waiting for initial process output |
| `--timeout` | | `5000` | Initial output wait timeout in milliseconds |

### `tuistory snapshot`
Capture the terminal buffer, including retained scrollback, as text. Insert the cursor marker at its terminal cell without splitting Unicode characters. Filters replace nonmatching cells with spaces; foreground/background filters match explicit cell colors, not screenshot theme defaults.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--trim` | | `false` | Strip trailing whitespace and empty rows |
| `--json` | | `false` | Output JSON object with metadata (`text`, `session`, `dead`, `exitCode`) |
| `--immediate` | | `false` | Capture immediately without waiting for terminal idle state |
| `--bold` | | `false` | Extract only bold text cells (others replaced by spaces) |
| `--italic` | | `false` | Extract only italic text cells |
| `--underline` | | `false` | Extract only underlined text cells |
| `--fg` | | `""` | Extract cells matching foreground `#RRGGBB` (case-insensitive) |
| `--bg` | | `""` | Extract cells matching background `#RRGGBB` (case-insensitive) |
| `--no-cursor` | | `false` | Hide cursor marker (`█`) in snapshot output |

### `tuistory read`
Read process output stream since previous read call (advancing cursor), or read full buffer. Strips ANSI escape codes.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--all` | | `false` | Return entire buffered output (up to 1MB) without advancing read cursor |
| `--trim` | | `false` | Trim trailing whitespace from output |
| `--follow` | | `false` | Block until new output or process exit; report timeout if a live process stays silent |
| `--timeout` | | `5000` | Timeout for `--follow` in milliseconds |

### `tuistory wait <pattern>`
Poll terminal output until text or regex pattern appears. Return raw-output context up to 10 lines before and after the first matching line; if the raw buffer has no match, return the trimmed terminal text.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--timeout` | | `5000` | Maximum wait duration in milliseconds |

String patterns match literally. Use `/pattern/flags` for Go regular expressions, such as `/[0-9]+/` or `/ready|listening/i`. Supported flags are `i` (case-insensitive), `m` (line anchors), `s` (dot matches newlines), and `g` (accepted; searches already start fresh and click finds all matches). Duplicate flags, unsupported flags (including `u` and `y`), and invalid expressions return a regex error immediately. A leading slash without a closing slash remains literal text.

### `tuistory wait-idle`
Wait until the process produces no PTY output for ~200ms, indicating rendering has stabilized.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--timeout` | | `500` | Maximum wait duration in milliseconds |

### `tuistory type <text>`
Type text character by character with 1ms delay between strokes to trigger autocomplete and search handlers.

Closed sessions and exited processes return an error, including when text is empty.

### `tuistory press <key> [...keys]`
Send a key or key combination (chord) to the terminal.

- **Modifiers**: `ctrl`, `alt`, `shift`, `meta`
- **Navigation**: `up`, `down`, `left`, `right`, `home`, `end`, `pageup`, `pagedown`
- **Actions**: `enter`, `esc`, `tab`, `space`, `backspace`, `delete`, `insert`
- **Function keys**: `f1` through `f12`
- **Alphanumeric & Punctuation**: `a`-`z`, `0`-`9`, symbols

```bash
tuistory -s app press enter
tuistory -s app press ctrl c
tuistory -s app press alt f4
tuistory -s app press tab
```

### `tuistory click <pattern>`
Search visible terminal text for a string or `/regex/` pattern and simulate an SGR mouse click at its position.

Use the same regex syntax and flags as `wait`. Click searches each visible terminal line separately, so its regex matches stay within one line.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--first` | | `false` | Click first occurrence if multiple matches found |
| `--timeout` | | `5000` | Timeout in milliseconds waiting for pattern |

### `tuistory click-at <x> <y>`
Send mouse click at 0-based coordinate `(x, y)`: `tuistory -s app click-at 10 5`.

### `tuistory scroll <direction> [lines]`
Send mouse wheel scroll events up or down: `tuistory -s app scroll down 5`.

The optional line count is a nonnegative integer, defaulting to 1 only when omitted. An explicit zero sends no input. Negative and noninteger counts return an error before sending input.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--x` | | center | X coordinate for scroll |
| `--y` | | center | Y coordinate for scroll |

Coordinates must be nonnegative and are 0-based. Omitted coordinates select the center; explicitly passing zero selects the terminal edge. Negative coordinates return an error before sending input.

### `tuistory resize <cols> <rows>`
Resize terminal dimensions and send `SIGWINCH` to running process: `tuistory -s app resize 160 50`.

Columns must be 2 through 65535 and rows must be 1 through 65535. Invalid dimensions return an error without changing the PTY, screen, or stored dimensions; zero is invalid for resize.

Closed sessions and exited processes return an error without changing the screen or stored dimensions.

### `tuistory screenshot`
Render terminal buffer to a PNG image file and print output path to stdout.

Relative `--output` paths are resolved in the caller's working directory. The printed path is absolute. Omit `--output` to create a temporary PNG in the daemon's temporary directory.

Include retained scrollback and trim trailing empty rows. Preserve ANSI colors, bold, italic, inverse, faint, underline, and strikethrough, with bundled CJK and Nerd icon fallback fonts. A blank buffer returns `no content to render`. Width crops or extends the canvas without changing terminal cell spacing; pixel ratio scales the rendered image. Reject final canvases, individual font glyph masks, or native geometry cell rasters larger than 67,108,864 pixels before bitmap allocation, including rasters cropped by a smaller canvas. Font drawing positions or translated glyph bounds outside the renderer's native coordinate range also return an error.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--output` | `-o` | temp file | PNG file destination path |
| `--width` | | auto | Image width in pixels |
| `--font-size` | | `14` | Font size in pixels |
| `--line-height`| | `1.5` | Line height multiplier |
| `--background`| | `#1a1b26` | Background color hex |
| `--foreground`| | `#c0caf5` | Text color hex |
| `--pixel-ratio`| | `1` | Scaling ratio (use 2 for HiDPI) |
| `--padding` | | `2` | Outer frame padding in terminal cells |
| `--frame-color` | | auto | Frame color hex; otherwise detect the dominant terminal edge background |
| `--immediate` | | `false` | Do not wait for idle before capturing |

### `tuistory capture-frames <key> [...keys]`
Send key(s) and immediately capture rapid text frames as a JSON array to detect layout shifts and transitions:
`tuistory -s app capture-frames tab --count 5 --interval 20`.

Count must be a positive integer. Interval must be an integer from 0 through 9223372036854 milliseconds, including zero for consecutive snapshots. Invalid values return an error before sending keys. Frames are collected incrementally; closing the session interrupts capture and its interval wait with an error.

Closed sessions and exited processes return an error before capture, including when the key list contains only modifiers.

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--session`, `-s` | required | Target session name |
| `--count` | `5` | Number of snapshots |
| `--interval` | `10` | Milliseconds between snapshots |

### `tuistory restart`
Gracefully restart a session (SIGINT -> SIGTERM if needed) and relaunch with identical command, cwd, dimensions, and environment.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--timeout` | | `5000` | Graceful shutdown timeout in milliseconds |
| `--no-wait` | | `false` | Skip waiting for initial output after relaunch |

### `tuistory close`
Terminate the PTY process, escalate to process group SIGKILL if necessary, and remove session from daemon.

The command waits for owned process termination and terminal I/O cleanup. SIGTERM is followed by SIGKILL after a two-second grace period when process groups remain alive.

### `tuistory sessions`
List sessions with status, start time, cwd, command, and dimensions, sorted newest first. In human mode the listing is formatted with color. In agent mode each session is a compact JSON line with fields `name`, `command`, `cwd`, `cols`, `rows`, `dead`, and `startedAt` (Unix milliseconds). An empty listing prints `No active sessions`. Pass `--json` for an array: compact in agent mode, indented in human mode, and `[]` when empty.

### `tuistory logfile`
Print the path to the daemon log file (`/tmp/tuistory/relay-server.log`).

### `tuistory daemon-stop`
Stop the background relay server and terminate all associated sessions and child processes.

Shutdown rejects new launch/restart operations and waits for termination, including SIGKILL escalation, before releasing the daemon port and returning `Daemon stopped`.

### `tuistory attach [-s <name>]`
Interactive full-screen TUI for humans to view live output and send keystrokes.
- Double `Ctrl+C` (within 450ms): detach and keep process running.
- Double `Ctrl+X` (within 450ms): kill process and detach.
*(Agents must not use attach; use `snapshot`, `read`, and `wait`.)*

### `tuistory skill`
Print this operational guide verbatim. Accept no positional arguments. Help, version, and skill commands run without starting the daemon; in agent mode every public help path begins with the alert to read this guide.

### `tuistory --version`
Print only the version string and a newline.

## Environment Variables

| Variable | Purpose |
| :--- | :--- |
| `TUISTORY_PORT` | Relay server TCP port (default: `19977`) |
| `TUISTORY_LOG_FILE_PATH` | Daemon log file path override |
| `TUISTORY_SESSION` | Set in child processes to indicate running inside tuistory |
| `TRAFORO_URL` / `SIGILLO` | Triggers passthrough mode, running child in foreground |
| `AGENT` | Set to `1`, `true`, or `yes` (case-insensitive) for agent help and to disable auto-attach |
| `AI_AGENT` | Any nonempty agent name enables agent help and disables auto-attach; an empty value is ignored |
| `TMPDIR` | Override the temporary directory used for daemon logs, PID/lock files, and default screenshots |

The daemon selects agent output and help from each request's environment. Agent launch/reuse diagnostics and silent-process warnings are compact single lines; changing one caller's agent mode does not change other callers. Terminal device/status query replies are forwarded to the child through its PTY.

The client checks both daemon protocol identity and release version. An incompatible TypeScript daemon is replaced even when its release version is higher. Stop only sessions and daemons you are authorized to stop.
