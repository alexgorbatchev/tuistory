---
name: tuistory
description: Use when launching, inspecting, waiting on, and interacting with background terminal sessions, dev servers, CLIs, or TUIs.
author: alexgorbatchev
metadata:
  created_on: 2026-04-14 12:00
  last_modified: 2026-10-02 12:31
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

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | auto | Session name (`<cwd-basename>-<hash>-<command>`) |
| `--cols` | | `120` | Terminal columns |
| `--rows` | | `36` | Terminal rows |
| `--cwd` | | caller cwd | Working directory for child process |
| `--env` | | `[]` | Environment variable `KEY=VAL` (repeatable) |
| `--background` | | `false` | Run in background without attaching |
| `--no-wait` | | `false` | Skip waiting for initial process output |
| `--timeout` | | `5000` | Initial output wait timeout in milliseconds |

### `tuistory snapshot`
Capture the current terminal screen buffer as clean text.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--trim` | | `false` | Strip trailing whitespace and empty rows |
| `--json` | | `false` | Output JSON object with metadata (`text`, `session`, `dead`, `exitCode`) |
| `--immediate` | | `false` | Capture immediately without waiting for terminal idle state |
| `--bold` | | `false` | Extract only bold text cells (others replaced by spaces) |
| `--italic` | | `false` | Extract only italic text cells |
| `--underline` | | `false` | Extract only underlined text cells |
| `--fg` | | `""` | Extract only text cells matching foreground color hex |
| `--bg` | | `""` | Extract only text cells matching background color hex |
| `--no-cursor` | | `false` | Hide cursor marker (`█`) in snapshot output |

### `tuistory read`
Read process output stream since previous read call (advancing cursor), or read full buffer. Strips ANSI escape codes.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--all` | | `false` | Return entire buffered output (up to 1MB) without advancing read cursor |
| `--trim` | | `false` | Trim trailing whitespace from output |
| `--follow` | | `false` | Block until new output arrives, then return it |
| `--timeout` | | `5000` | Timeout for `--follow` in milliseconds |

### `tuistory wait <pattern>`
Poll terminal output until text or regex pattern appears. Returns matching context (up to 10 lines before and after match).

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--timeout` | | `5000` | Maximum wait duration in milliseconds |

*Note: String patterns match literally. Use `/pattern/flags` syntax for regex (e.g. `/[0-9]+/`, `/ready\|listening/i`).*

### `tuistory wait-idle`
Wait until the process produces no PTY output for ~200ms, indicating rendering has stabilized.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--timeout` | | `500` | Maximum wait duration in milliseconds |

### `tuistory type <text>`
Type text character by character with 1ms delay between strokes to trigger autocomplete and search handlers.

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

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--first` | | `false` | Click first occurrence if multiple matches found |
| `--timeout` | | `5000` | Timeout in milliseconds waiting for pattern |

### `tuistory click-at <x> <y>`
Send mouse click at 0-based coordinate `(x, y)`: `tuistory -s app click-at 10 5`.

### `tuistory scroll <direction> [lines]`
Send mouse wheel scroll events up or down: `tuistory -s app scroll down 5`.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--x` | | center | X coordinate for scroll |
| `--y` | | center | Y coordinate for scroll |

### `tuistory resize <cols> <rows>`
Resize terminal dimensions and send `SIGWINCH` to running process: `tuistory -s app resize 160 50`.

### `tuistory screenshot`
Render terminal buffer to a PNG image file and print output path to stdout.

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
| `--immediate` | | `false` | Do not wait for idle before capturing |

### `tuistory capture-frames <key> [...keys]`
Send key(s) and immediately capture rapid text frames as a JSON array to detect layout shifts and transitions:
`tuistory -s app capture-frames tab --count 5 --interval 20`.

### `tuistory restart`
Gracefully restart a session (SIGINT -> SIGTERM if needed) and relaunch with identical command, cwd, dimensions, and environment.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session` | `-s` | (required) | Target session name |
| `--timeout` | | `5000` | Graceful shutdown timeout in milliseconds |
| `--no-wait` | | `false` | Skip waiting for initial output after relaunch |

### `tuistory close`
Terminate the PTY process, escalate to process group SIGKILL if necessary, and remove session from daemon.

### `tuistory sessions`
List active sessions with status, start time, cwd, command, and dimensions. Pass `--json` for machine parseability.

### `tuistory logfile`
Print the path to the daemon log file (`/tmp/tuistory/relay-server.log`).

### `tuistory daemon-stop`
Stop the background relay server and terminate all associated sessions and child processes.

### `tuistory attach [-s <name>]`
Interactive full-screen TUI for humans to view live output and send keystrokes.
- Double `Ctrl+C` (within 450ms): detach and keep process running.
- Double `Ctrl+X` (within 450ms): kill process and detach.
*(Agents must not use attach; use `snapshot`, `read`, and `wait`.)*

### `tuistory skill`
Print this operational guide verbatim.

## Environment Variables

| Variable | Purpose |
| :--- | :--- |
| `TUISTORY_PORT` | Relay server TCP port (default: `19977`) |
| `TUISTORY_LOG_FILE_PATH` | Daemon log file path override |
| `TUISTORY_SESSION` | Set in child processes to indicate running inside tuistory |
| `TRAFORO_URL` / `SIGILLO` | Triggers passthrough mode, running child in foreground |
| `AGENT` / `AI_AGENT` | Disables interactive auto-attach for scripts and agents |
