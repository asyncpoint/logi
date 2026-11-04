# logi — Log Inspector

**logi** is a fast, terminal-first log inspector for developers. It opens a quiet static log, a multi-gigabyte file, a log that is still growing, a directory of rotating logs, or the output of `docker logs` and `kubectl logs`, and keeps the same keyboard-driven view for all of them.

Its design goal is to make log inspection fast and useful without turning the terminal into a heavyweight application. Live retention is bounded, no persistent index is written by default, records are parsed incrementally, and log content is treated as untrusted terminal data.

> **New to logi?** Read [Quick start](#quick-start) and [Common tasks](#common-tasks), then keep the [How-to guide](docs/HOWTO.md) open for everything else.

## Contents

- [Highlights](#highlights)
- [Install](#install)
- [Quick start](#quick-start)
- [Common tasks](#common-tasks)
- [Static and live modes](#static-and-live-modes)
- [Search and filtering](#search-and-filtering)
- [Time windows](#time-windows)
- [Several files, directories and pipes](#several-files-directories-and-pipes)
- [Docker and Kubernetes](#docker-and-kubernetes)
- [Log formats](#log-formats)
- [Keyboard controls](#keyboard-controls)
- [Command-line options](#command-line-options)
- [Configuration](#configuration)
- [Performance](#performance)
- [Security and terminal safety](#security-and-terminal-safety)
- [Versioning and stability](#versioning-and-stability)
- [Development](#development)
- [License](#license)

## Highlights

- Opens very large files through lazy, offset-based access instead of loading them into memory.
- Follows growing, rotated, truncated and re-created files with bounded memory.
- Follows one directory, the newest file in it, or every matching file at once, merged and colour-coded.
- Reads `docker logs`, `docker compose logs` and `kubectl logs` from a pipe, with one colour per service or pod.
- Understands plain text, JSON Lines, zap, pino, logrus, slog and klog output, Docker and Kubernetes container logs, and NCache logs.
- Joins multi-line records such as stack traces, with size and line limits.
- Searches literally, by regular expression, or by structured field, with matches highlighted in the column that contains them.
- Filters with a compact query language (`severity:>=warn`, `thread=43`, `since:15m`) or with startup flags.
- Shows any record in a detail view; JSON is pretty-printed.
- Selects records and copies them to the clipboard.
- Offers bookmarks, error and warning navigation, column hiding, a command palette, a file picker, mouse input and rebindable keys.
- Ships `logigen`, a synthetic log generator for testing and measurement.

## Install

**Download a release.** Every version on the GitHub *Releases* page carries archives built automatically by GitHub Actions for Windows, Linux and macOS, each for amd64 (x64) and arm64:

| System | amd64 (x64) | arm64 |
|---|---|---|
| Linux | `logi_<version>_linux_amd64.tar.gz` | `logi_<version>_linux_arm64.tar.gz` |
| macOS | `logi_<version>_darwin_amd64.tar.gz` | `logi_<version>_darwin_arm64.tar.gz` (Apple silicon) |
| Windows | `logi_<version>_windows_amd64.zip` | `logi_<version>_windows_arm64.zip` |

Each archive holds the program (`logi`, or `logi.exe` on Windows), the README, the license, the changelog and the `docs/` folder with the how-to guide and an example configuration. Unpack it and put the program on the `PATH`. A `SHA256SUMS` file lists the checksum of every archive:

```bash
sha256sum -c SHA256SUMS --ignore-missing                  # Linux
shasum -a 256 -c SHA256SUMS --ignore-missing              # macOS
tar -xzf logi_1.1.0_linux_amd64.tar.gz                    # unpack (a .zip on Windows)
```

```powershell
(Get-FileHash .\logi_1.1.0_windows_amd64.zip -Algorithm SHA256).Hash   # compare with SHA256SUMS
```

Every push to the repository also builds all six archives; they appear under **Actions → the run → Artifacts**, so a build is available before a release is cut.

To build from source (Go 1.25 or newer, see `go.mod`):

```bash
go build -trimpath -ldflags "-s -w -X main.version=v1.0.0" -o logi ./cmd/logi
```

Cross-compiling needs no extra tools because logi is pure Go:

```bash
# a Linux binary, built on any machine (PowerShell: $env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0")
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o logi-linux-amd64 ./cmd/logi
```

`make build` builds for the current platform and `make dist` cross-compiles the release targets. Check the result with `logi --version`.

## Quick start

```bash
logi application.log                    # browse a file (read-only, any size)
logi -f application.log                 # follow it as it grows, like tail -f
logi -n 500 application.log             # follow, starting from the last 500 lines
logi --since 15m --level warn,error app.log   # the last 15 minutes of warnings and errors
logi -f logs/api.log logs/web.log       # several files, merged and colour-coded
logi -f /var/log/myapp/                 # the newest file in a directory, across rollovers
kubectl logs deploy/web -f | logi       # read a pipe
```

Once the screen is open:

| Key | Does |
|---|---|
| `↑` `↓` / `j` `k`, `PgUp` `PgDn`, `g` `G` | Move, page, jump to top / bottom |
| `/` | Search (results appear while typing); `n` / `N` next / previous match |
| `f` | Filter, for example `severity:>=warn`; `c` clears it |
| `Enter` | Open the detail view of the current record |
| `v` … `v`, then `y` | Select a range of records and copy it |
| `Alt+E` | Jump to the next error |
| `?` | Help and every key; `:` opens the command palette |
| `q` | Quit |

`logi --help` lists every option, grouped by purpose. Multi-character options use `--`; the short forms are `-f` (`--follow`), `-n` (`--lines`), `-V` (`--version`) and `-h` (`--help`). Choices such as `--dir-mode` and `--color` ignore case, and a mistyped option or value gets a suggestion.

## Common tasks

Every command below works as written once the file names are replaced with real ones. The [How-to guide](docs/HOWTO.md) explains each task step by step.

**Look at a file**

```bash
logi application.log                          # browse; the file is read lazily, so size does not matter
logi --parser json service.jsonl              # force a parser (default: detected automatically)
logi --hide-columns id,source app.log         # start with some columns hidden
```

**Watch a log live**

```bash
logi -f application.log                       # follow; 8 MiB of history is loaded first
logi -n 200 application.log                   # start from the last 200 lines (implies -f)
logi -f --from-start application.log          # read the whole file first, then follow
logi -f --backlog 0 application.log           # only records written from now on
logi -f --poll application.log                # network drives where file notifications are unreliable
```

**Find the problem**

```bash
logi --level error application.log                      # only errors (and worse)
logi --level warn,error --contains timeout app.log      # warnings and errors that mention "timeout"
logi --since 30m app.log                                # the last 30 minutes, measured from the newest record
logi --since 09:00 --until 09:30 app.log                # a time window
logi --field host=server01 --exclude "health check" app.json
logi --filter 'severity:>=warn message:"connection refused" -source:probe' app.log
```

In the screen, `Alt+E` / `Alt+Shift+E` jump to the next / previous error and `Alt+Shift+W` / `Alt+W` to the next / previous warning.

**Watch several sources**

```bash
logi -f logs/api.log logs/web.log logs/worker.log        # merged, one colour per file
logi -f --dir-mode all --glob '*.log' logs/              # every *.log file in a directory
logi -f logs\*.log                                       # wildcards also work in cmd.exe and PowerShell
docker compose logs -f | logi                            # one colour per service
kubectl logs -f -l app=web --prefix --all-containers | logi   # one colour per pod and container
```

**Copy evidence into a ticket**

In the screen: move to the first record, press `v`, move to the last record, press `v` again, then `y`. The raw text of all selected records is on the clipboard, one per line, without colour codes. With no selection, `y` copies the current record. See [Selecting and copying](docs/HOWTO.md#selecting-and-copying) for terminal requirements.

**Make settings permanent**

```bash
export LOGI_FOLLOW=true LOGI_COLOR=never      # environment variables
logi --config ./team-logi.yaml app.log        # or a config file (see docs/config.example.yaml)
```

## Static and live modes

logi keeps two modes apart on purpose.

**Static mode** (the default for a file) reads records lazily by file offset. A small index of block positions is built in the background while the file is already browsable, and a small cache holds recently shown blocks. A multi-gigabyte file is never loaded into memory. Static mode is meant for files that are finished; use `-f` for a file that is still being written.

**Live mode** (`-f`) follows the file incrementally into a bounded ring. Both the number of records and the approximate retained bytes are capped. Truncation, rotation and re-creation of the file are handled.

```text
static file                         live file
    ↓                                   ↓
offset index + small block cache    incremental tailer
    ↓                                   ↓
visible records                     bounded ring (records + bytes)
                                        ↓
                                    visible records
```

## Search and filtering

Search and filtering are separate. **Search** (`/`) moves the cursor between matches in the current view. **Filtering** (`f`) hides everything that does not match.

**Search** starts as soon as typing starts and always searches from where it began; `Enter` keeps the match, `Esc` returns to the starting point, and `n` / `N` repeat. There are three modes, switched while the search box is open:

| Mode | How | Matches |
|---|---|---|
| literal (default) | — | the text, ignoring case |
| regular expression | `Alt+R` | RE2 syntax, for example `timeout\s+\d+` |
| structured field | `Alt+F` | `key=value`, for example `thread=43` |

`Alt+C` toggles case sensitivity. The initial mode can be set with `--search-mode`. Matches are highlighted in the column that contains them.

**Filters** are space-separated terms that are ANDed together; a leading `-` negates a term:

```text
severity:error                 exactly that level
severity:>=warn                that level or worse (also >, <=, <)
severity:warn,error            any of these levels
source:cache.log               source (file or service) name contains the text
message:"connection failed"    message contains the phrase
thread=43                      structured field equals the value (any field name)
re:timeout\s+\d+               regular expression on the record text
since:15m  until:"2026-10-15 14:30"     time window (see below)
"exact phrase"                 record text contains the phrase
word                           record text contains the word
-word   -severity:debug        negation
```

The same expressions can be given at start with `--filter`, and the common ones have their own flags, all applied before the screen appears:

```bash
logi -f app.log --level warn,error --contains "connection refused"
logi -f --dir-mode all --glob '*.log' logs/ --regex "timeout|failed"
logi app.json --field host=server01 --exclude "health check"
```

`--level` takes comma-separated severities and can be repeated (they are ORed). Every other flag adds an AND condition, and `--contains`, `--regex`, `--field` and `--exclude` can be repeated. `--contains` and `--exclude` match literal text ignoring case, `--regex` uses RE2, and `--field` compares a structured field. Flags combine with `--filter`. An invalid filter is rejected before the screen opens. Severity names are normalised across logging stacks (`WARNING`, `WRN`, `warn` and `Warning` are all warnings).

## Time windows

`--since` and `--until` (or `since:` / `until:` in a filter) restrict the view to a window.

- **Durations** (`15m`, `2h`, `1d`, `1h30m`) count back from the **newest record in the log**, not from the clock. "The last 15 minutes" therefore also works on an old file and on logs written without a time zone.
- **Absolute times** are `14:30` (that time on the newest record's date), `2026-10-15`, `2026-10-15 14:30`, or a full timestamp such as `2026-10-15T14:30:00Z`. A bare date given to `--until` includes that whole day.
- Times without a zone are compared with what the log shows; times with a zone are compared as exact instants.
- Records without a timestamp are kept.
- For piped input there is no file to measure from, so relative times use the clock.

## Several files, directories and pipes

**Several files.** Given several files from one directory, logi follows them together in one merged, colour-coded view (the same view as `--dir-mode all`). Wildcards such as `logs\*.log` are expanded by logi itself when the shell does not do it (cmd.exe and PowerShell do not).

**A directory.** Without `-f`, logi opens the newest matching file, and the file picker (`Ctrl+O`) switches to another. With `-f` it follows:

```bash
logi -f /var/log/myapp/                                  # the newest file; rolls over to new files
logi -f --dir-mode all --glob '*.log' logs/              # every matching file at once
```

In `--dir-mode all` every file gets its own colour (like `docker compose logs` does for services), and the screen gives the message as much width as possible:

- The **header line** names, in the file's colour, the file the cursor row came from.
- A thin **colour bar** in the row gutter shows which file every row came from. A bookmarked row shows its ★ there in the same colour.
- The **source** column is hidden by default. Bring it back with `--show-columns source` (or press `X` to show every column). When the files have different formats (the header says `mixed`) and some are NCache cache logs, an **event** column takes its place.

Colours are stable for the session: files present at startup are coloured in alphabetical order, files that appear later take the next colour, and a file never changes colour. The palette avoids red (reserved for errors) and has ten colours. With more than ten files the colours repeat; `--source-markers` marks the repeats with a shape (◆ ▲ ■ ●). `--source-gutter` is `auto` by default (the bar shows while the source column is hidden); use `--source-gutter=on` or `=off` to force it.

File identity is tracked, so renamed rotation files are not read twice. Hidden files, sub-directories and compressed archives (`.gz`, `.zip`, `.bz2` …) are never opened. Numbered rotation leftovers such as `app.log.1` are skipped unless `--glob` is given.

**Pipes.** `-`, or any piped input, reads standard input. The keyboard keeps working because logi reads keys from the terminal. When the producing command ends, the view stays open and shows that the input has ended.

## Docker and Kubernetes

logi reads live container logs from a pipe and understands what the tooling puts in front of the lines:

```bash
docker logs -f web 2>&1 | logi                          # 2>&1: docker prints the container's stderr on stderr
docker compose logs -f | logi                           # `web-1  | message`: one source per service
kubectl logs -f deploy/web | logi
kubectl logs -f -l app=web --prefix --all-containers | logi   # `[pod/web-7d9f/web] message`: one source per pod/container
```

- **Services and pods become sources.** The `web-1  |` and `[pod/…]` prefixes are removed and used as the record's source: each service gets its own colour in the row gutter and its name in the header, and `source:web-1` filters to it. Stack traces stay with their own service even when output is interleaved.
- **`docker logs -t` and `kubectl logs --timestamps`** stamp every line; when the application also timestamps its lines, the extra stamp is removed so multi-line records stay together.
- **Colour codes** (Spring Boot, Node, pytest …) are removed, so levels and timestamps are found.
- **Structured logs** from zap, pino, logrus, slog and klog, plain text, and Docker's `json-file` format are parsed. On a node, `logi -f /var/lib/docker/containers/<id>/<id>-json.log` and the CRI files under `/var/log/pods` work directly.
- logi does not reconnect when the command ends (the container stops, the pod restarts).
- **Windows PowerShell:** `2>&1` can wrap a native command's stderr lines in error records. Use `cmd /c "docker logs -f web 2>&1 | logi"` instead.
- Not handled: `stern`'s `pod container message` layout, CRI's partial-line (`P`) joining, and prefixes in *saved* files (prefix handling is for piped input).

## Log formats

`--parser auto` (the default) picks the best match from a sample of the input and falls back to plain text. List the profiles with `logi --list-parsers`:

| Profile | For |
|---|---|
| `generic` | plain text with timestamp and severity heuristics |
| `json` | JSON Lines, one object per line (zap, pino, logrus, slog …) |
| `cri` | Kubernetes and containerd container log files |
| `ncache` | NCache cache logs |
| `ncache-events` | NCache event logs |

```bash
logi --parser generic server.log           # skip detection and treat the file as plain text
logi --parser ncache server.log
logi --disable-plugin ncache server.log    # turn the NCache plugin off entirely
logi --no-multiline app.log                # one record per physical line, no stack-trace joining
```

Records that start with a timestamp absorb the following lines that do not (stack traces), up to `--max-record-size` (default 64 KiB) and `--max-lines` (default 2000); longer records are marked truncated, and byte offsets stay exact.

**NCache.** The NCache parser is built in and enabled by default. It recognises the cache-log and event-log layouts, reads the real header widths, validates rows against those boundaries, and realigns against data rows when an export has imperfect padding. Trailing `END` terminators are handled and stack traces fold into their record. The detail view shows the parsed fields instead of the raw tab-separated line.

## Keyboard controls

Press `?` in logi to see the same list. Every key can be rebound (see [Configuration](#configuration)).

| Action | Keys |
|---|---|
| Down / Up | `↓` `j` / `↑` `k` |
| Page down / up | `PgDn` `Space` `Ctrl+D` / `PgUp` `b` `Ctrl+U` |
| Go to top / bottom | `Home` `g` / `End` `G` |
| Search | `/` `Ctrl+F` |
| Next / previous match | `n` `F3` / `N` `Shift+F3` |
| Next / previous error | `Alt+E` / `Alt+Shift+E` |
| Next / previous warning or worse | `Alt+Shift+W` / `Alt+W` |
| Jump to record ID | `Ctrl+J` |
| Toggle bookmark, next, previous | `m`, `]`, `[` |
| Select records (start, then extend) / clear selection | `v` / `V` |
| Copy records | `y` |
| Filter / clear filter | `f` `Ctrl+L` / `c` `Ctrl+K` |
| Inspect record (detail view) | `Enter` `i` |
| Pause / resume following (live mode) | `p` `Ctrl+T` |
| Previous / next column | `h` `←` `Shift+Tab` / `l` `→` `Tab` |
| Hide current column / show all columns | `x` / `X` |
| Open a file / pick a file from the directory | `o` `Ctrl+P` / `Ctrl+O` |
| Command palette | `:` `F1` `Ctrl+Shift+P` |
| Help | `?` |
| Close overlay or cancel input | `Esc` |
| Quit | `q` `Ctrl+C` |

In the search box: `Alt+R` regular expression, `Alt+F` field, `Alt+C` case sensitivity. In the detail view: `←` `→` (or `[` `]`) step to the previous or next record, `↑` `↓` scroll, `y` copies the record, `Esc` closes. The mouse wheel scrolls, a click selects a row, and a click on a column header selects that column.

Navigation works on stable record IDs and on the current filtered view. Bookmarks and selections are kept as record IDs for the session and are cleared when another file is opened; the live bookmark set is capped at 10,000 entries. The `Alt+E` and `Alt+W` scans run outside the interface loop, so the screen stays responsive on large files.

`y` copies the raw text of the selection in view order (or the current record when nothing is selected), one record per line, without terminal colour codes. The text is sent with an OSC 52 escape sequence, so the terminal must allow it: most modern terminals do, tmux needs `set -g set-clipboard on`, and some terminals ask for permission first. One copy is capped at 512 KiB and is cut at a record boundary; the status line says so.

## Command-line options

```text
logi [options] <file>
logi [options] <directory>
logi [options] <file> <file> ...
<command> | logi [options]
```

Boolean options take `=false` to switch off, for example `--follow=false`.

| Option | Description |
|---|---|
| `-f`, `--follow`, `--live` | Follow the file as it grows (`--live` is an alias). |
| `-n`, `--lines <n>` | Follow, starting with only the last `n` lines of each file. Implies `--follow`; cannot be combined with `--backlog` or `--from-start`. |
| `--from-start` | Follow, but read the whole file first. |
| `--backlog <size>` | History loaded before following, e.g. `512KiB`, `8MiB`; `0` = only new data (default 8 MiB). |
| `--buffer <records>` | Records kept in memory in live mode (default 50000). |
| `--buffer-bytes <size>` | Memory budget for kept records; `0` = unlimited (default 256 MiB). |
| `--poll` | Check the file on a timer instead of using file-system notifications (network drives). |
| `--dir-mode <latest\|all>` | Directory strategy: follow the newest file and roll over, or follow every matching file (default `latest`). |
| `--glob <patterns>` | Which files count in directory mode, comma separated, e.g. `'*.log,*.txt'` (default: all log-like files). |
| `--max-files <n>` | Most files followed at once in `all` mode (default 32). |
| `--source-gutter[=auto\|on\|off]` | File-colour bar in the row gutter, `all` mode (default `auto`). |
| `--source-markers` | With more than ten files, mark repeated colours with a shape (◆ ▲ ■ ●). |
| `--filter <query>` | Full filter query; terms are ANDed. |
| `--level <names>` | Only these severities, e.g. `warn,error`; repeatable, ORed. |
| `--contains <text>` | Only records containing this text, ignoring case; repeatable, ANDed. |
| `--regex <pattern>` | Only records matching this RE2 expression; repeatable, ANDed. |
| `--field <key=value>` | Only records whose field equals the value; repeatable, ANDed. |
| `--exclude <text>` | Drop records containing this text; repeatable. |
| `--since <time>` | Only records from this time on: `15m`, `2h`, `1d` (back from the newest record), `14:30`, `2026-10-15 14:30`. |
| `--until <time>` | Only records up to this time (same formats; a bare date includes that whole day). |
| `--search-mode <literal\|regex\|field>` | How in-app search reads typed text (default `literal`). |
| `--color <auto\|always\|never>` | Colours (default `auto`; `NO_COLOR` is honoured). |
| `--hide-columns <names>` | Columns to hide at start, e.g. `id,source`. |
| `--show-columns <names>` | Columns to show at start, e.g. `source`. |
| `--parser <name>` | `auto`, `generic`, `json`, `ncache`, `ncache-events`, … see `--list-parsers`. |
| `--no-multiline` | One record per physical line; no stack-trace joining. |
| `--max-record-size <size>` | Cut records longer than this (default 64 KiB). |
| `--max-lines <n>` | Cut multi-line records after this many lines (default 2000). |
| `--enable-plugin <name>` | Turn a built-in plugin on; repeatable or comma separated. |
| `--disable-plugin <name>` | Turn a built-in plugin off, e.g. `ncache`; repeatable or comma separated. |
| `--config <path>` | Read settings from this file. |
| `--list-parsers` | List the supported log formats and exit. |
| `-V`, `--version` | Print the version and exit. |
| `-h`, `--help` | Show help. |

Exit codes: `0` success (including `--help`, `--version`, `--list-parsers`), `1` a runtime failure (a file or session could not be opened), `2` a usage or configuration mistake.

## Configuration

Settings come from four places; later ones win:

```text
built-in defaults  →  config file  →  LOGI_* environment variables  →  command-line flags
```

The config file is a small YAML subset that logi only ever reads. It is looked for at `--config <path>`, then `$LOGI_CONFIG`, then `<user-config-dir>/logi/config.yaml` (`~/.config/logi/config.yaml` on Linux). Keys use snake_case (`limits.max_record_size`) where options use kebab-case (`--max-record-size`). Unknown keys are rejected so that typos are not silent. [`docs/config.example.yaml`](docs/config.example.yaml) lists every key.

```yaml
# ~/.config/logi/config.yaml
color: auto
buffer:
  capacity: 100000
live:
  backlog: 16MiB
search:
  mode: regex
keys:
  search: "ctrl+f, /"
```

Environment variables: `LOGI_CONFIG`, `LOGI_FOLLOW`, `LOGI_PARSER`, `LOGI_COLOR`, `LOGI_BUFFER`, `LOGI_DISABLE_PLUGINS`; `NO_COLOR` turns colours off.

## Performance

logi optimises the common log-viewing loop rather than being a generic data-processing framework:

- Streams and frames input incrementally.
- Bounds live retention by both records and bytes.
- Reads static files lazily instead of loading them.
- Rejects non-matching records on their raw text before parsing them, and scans large files on parallel workers with results delivered in order.
- Keeps parser allocations small, especially for JSON.
- Batches interface updates.
- Writes no persistent on-disk index by default.
- Caps pathological multi-line records.

`logigen` produces reproducible synthetic logs for measuring these properties:

```bash
logigen -n 1000000 -format json -o big.jsonl       # a million JSON records
logigen -live -rate 2000 -o live.log               # keep appending 2000 records a second (Ctrl+C to stop)
logigen -format ncache -n 50000 -o ncache.log      # NCache-style records
```

See [`BENCHMARKS.md`](BENCHMARKS.md) for measured figures.

## Security and terminal safety

Log content is treated as untrusted data. Terminal control sequences are removed as text is ingested and again before it is rendered, so a malicious log entry cannot clear the screen, move the cursor, or inject terminal commands through the interface.

## Versioning and stability

logi follows [semantic versioning](https://semver.org/) from 1.0.0. The command line, configuration keys, environment variables, filter language, exit codes and parser names will not change incompatibly within 1.x; the exact appearance of the interface and message wording may. See [`docs/STABILITY.md`](docs/STABILITY.md) for the full list.

## Development

The project targets the Go version declared in `go.mod` (1.25.0 or newer).

```bash
go test ./...          # unit, integration and scale tests
go test -race ./...
go vet ./...
make build             # binary for this platform
make dist              # release binaries for six platform/architecture pairs
make fuzz              # fuzz targets (parsers, framing, filter, search, sanitizer)
```

Real NCache fixtures and integration and performance fixtures live in the repository, so parser and large-file behaviour can be reproduced from a clean clone. See [`CONTRIBUTING.md`](CONTRIBUTING.md) before sending a change, and [`docs/TDD.md`](docs/TDD.md) and [`docs/ADR/`](docs/ADR/) for the technical baseline and design decisions.

## License

logi is released under the license in [`LICENSE`](LICENSE).
