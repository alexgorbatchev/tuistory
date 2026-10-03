A Go rewrite of the original [tuistory](https://github.com/remorses/tuistory) by [remorses](https://github.com/remorses). `tuistory` runs background dev servers and terminal commands in named pseudo-terminal sessions that AI agents can inspect, wait on, and type into while humans can attach to view live output.

# What It Does

- **Named background terminal sessions**: Spawns long-lived processes inside a background relay daemon that persists across tool calls and CLI invocations.
- **Reactive output waiting**: Replaces blind `sleep` with pattern-matching wait and debounce idle detection so scripts react the instant output settles.
- **Terminal screen capture**: Extracts full terminal buffers, style-filtered text (bold, italic, color), and pixel-perfect PNG screenshots.
- **Interactive input simulation**: Sends characters with per-keystroke timing, key chords, mouse clicks, and terminal resize events.
- **Human-in-the-loop attachment**: Lets humans attach interactively to live sessions with full-screen TUI streaming and keystroke forwarding.
- **Process tree lifecycle**: Reaps child and grandchild process groups on exit to prevent orphaned background servers.

# How It Works

- Starts or reuses the background relay daemon listening on `127.0.0.1:19977`.
- Spawns the specified command inside a virtual pseudo-terminal (PTY) with configurable rows and columns.
- Streams PTY output through an in-memory headless terminal emulator that tracks cursor position, screen dimensions, colors, and alternate buffers.
- Forwards commands (`read`, `wait`, `snapshot`, `type`, `press`, `click`, `restart`, `close`) to the daemon over local HTTP.
- Returns output and exit codes immediately to callers without blocking agent tools on long-running processes.

# How it Really Works

- The relay daemon runs detached on `127.0.0.1:19977` and pins its working directory to `$HOME` to prevent stale filesystem handles across directory deletions.
- Port ownership is verified before commands run; if an orphaned or unresponsive process squats on the port, the client terminates it and spawns a fresh daemon.
- Child processes are launched in dedicated POSIX sessions; closing or terminating a session signals the entire process group so background servers cannot survive as orphans.
- Output is buffered in an in-memory ring buffer (up to 1MB) and stripped of ANSI escape codes for `read`, while raw escape sequences are preserved for live WebSocket attach clients.
- The daemon enforces localhost-only security middleware that rejects requests containing `Origin` headers, cross-site fetch markers, or non-loopback `Host` headers to prevent browser DNS rebinding attacks.
- When `AGENT=1` is set, help and diagnostic outputs switch automatically to token-conservative structured key-value format.

# Installation

Download the prebuilt binary for your platform from the [latest release](https://github.com/remorses/tuistory/releases/latest), replacing `X.X.X` with the version shown on that page.

```bash
# macOS (Apple Silicon)
curl -sSL https://github.com/remorses/tuistory/releases/latest/download/tuistory_X.X.X_darwin_arm64.tar.gz | tar -xz -C ~/.local/bin

# Linux (x86_64)
curl -sSL https://github.com/remorses/tuistory/releases/latest/download/tuistory_X.X.X_linux_amd64.tar.gz | tar -xz -C ~/.local/bin
```

# Quick Start

```bash
# Launch a background session (session name auto-derived from cwd and command)
tuistory -- ./server --port 3000

# Wait reactively for the server to be ready
tuistory -s myapp-server wait "/ready|listening/i" --timeout 30000

# Read new process output
tuistory read -s myapp-server

# Capture the visible terminal screen
tuistory -s myapp-server snapshot --trim

# Close the session
tuistory -s myapp-server close
```

Sample Output:
```
Session "myapp-server" is now running in the background.

  command: ./server --port 3000
  cwd:     /home/user/myapp
  cols:    120
  rows:    36

The process is alive but you are not attached to it.
```

# Options & Flags

### Global Options

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--help` | `-h` | `false` | Display help screen and command tree |
| `--version` | `-v` | `false` | Display binary version |

### `tuistory [options] -- <command>` / `tuistory launch [command]`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session <name>` | `-s` | auto | Session name (defaults to `<cwd-basename>-<hash>-<command>`) |
| `--cols <n>` | | `120` | Terminal columns |
| `--rows <n>` | | `36` | Terminal rows |
| `--cwd <path>` | | caller cwd | Working directory for child process |
| `--env <k=v>` | | `[]` | Environment variable (repeatable) |
| `--background` | | `false` | Run in background without attaching |
| `--no-wait` | | `false` | Skip waiting for initial process output |
| `--timeout <ms>` | | `5000` | Initial output wait timeout in milliseconds |

### `tuistory snapshot`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session <name>` | `-s` | (required) | Target session name |
| `--trim` | | `false` | Strip trailing whitespace and empty rows |
| `--json` | | `false` | Output structured JSON metadata |
| `--immediate` | | `false` | Capture immediately without waiting for terminal idle state |
| `--bold` | | `false` | Extract only bold text cells |
| `--italic` | | `false` | Extract only italic text cells |
| `--underline` | | `false` | Extract only underlined text cells |
| `--fg <color>` | | `""` | Extract only text cells matching foreground color hex |
| `--bg <color>` | | `""` | Extract only text cells matching background color hex |
| `--no-cursor` | | `false` | Hide cursor marker (`█`) in snapshot output |

### `tuistory read`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session <name>` | `-s` | (required) | Target session name |
| `--all` | | `false` | Return entire buffered output without advancing read cursor |
| `--trim` | | `false` | Trim trailing whitespace from output |
| `--follow` | | `false` | Block until new output arrives |
| `--timeout <ms>` | | `5000` | Timeout for `--follow` in milliseconds |

### `tuistory wait <pattern>`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session <name>` | `-s` | (required) | Target session name |
| `--timeout <ms>` | | `5000` | Maximum wait duration in milliseconds |

### `tuistory wait-idle`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session <name>` | `-s` | (required) | Target session name |
| `--timeout <ms>` | | `500` | Maximum wait duration in milliseconds |

### `tuistory screenshot`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session <name>` | `-s` | (required) | Target session name |
| `--output <path>` | `-o` | temp file | PNG file destination path |
| `--width <px>` | | auto | Image width in pixels |
| `--font-size <px>` | | `14` | Font size in pixels |
| `--line-height <n>`| | `1.5` | Line height multiplier |
| `--background <c>` | | `#1a1b26` | Background color hex |
| `--foreground <c>` | | `#c0caf5` | Text color hex |
| `--pixel-ratio <n>`| | `1` | Scaling multiplier for HiDPI |
| `--padding <cells>`| | `2` | Outer frame padding in terminal cells |
| `--immediate` | | `false` | Do not wait for idle before capturing |

### `tuistory click <pattern>`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session <name>` | `-s` | (required) | Target session name |
| `--first` | | `false` | Click first occurrence if multiple matches found |
| `--timeout <ms>` | | `5000` | Timeout in milliseconds waiting for pattern |

### `tuistory restart`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--session <name>` | `-s` | (required) | Target session name |
| `--timeout <ms>` | | `5000` | Graceful shutdown timeout in milliseconds |
| `--no-wait` | | `false` | Skip waiting for initial output after relaunch |

# Dev Script Convention

Wrap your project's dev server command with `tuistory --` so AI agents never hang on long-running processes and humans get auto-attached:

```just
# Example in a justfile
dev:
    tuistory -- ./server --port 3000
```

Or in shell scripts / task runners:

```bash
tuistory -- ./server --port 3000
```

- **Humans**: Running the dev command auto-attaches your terminal to the session with live output and keyboard interaction. Press `Ctrl+C` twice to detach while keeping the process running in the background.
- **Agents**: Running the dev command launches the process in the background and returns immediately, allowing the agent to inspect output with `read`, `wait`, and `snapshot` without blocking its turn.
- **Idempotency**: Running the command while a session is already alive reuses the running session instead of failing with port conflicts.

# Passthrough Mode

When running inside `TRAFORO_URL`, `SIGILLO`, or `TUISTORY_SESSION`, `tuistory` skips daemon creation and runs the command directly in the foreground with inherited stdio and signal forwarding.

# License

[MIT License](LICENSE) (c) 2026 Alex Gorbatchev
