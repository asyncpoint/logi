# logi how-to guide

This guide shows how to do things with logi, one task at a time. It starts with the basics and ends with tuning, troubleshooting and a cheat sheet. For a short introduction see the [README](../README.md); for every option in one table see [Command-line options](../README.md#command-line-options).

Commands are shown for a Unix shell. On Windows, cmd.exe and PowerShell run the same `logi` commands; where behaviour differs, a note says so.

## Contents

1. [Getting started](#getting-started)
2. [Reading the screen](#reading-the-screen)
3. [Browsing a file](#browsing-a-file)
4. [Following a log live](#following-a-log-live)
5. [Searching](#searching)
6. [Filtering](#filtering)
7. [Time windows](#time-windows)
8. [Finding errors and warnings](#finding-errors-and-warnings)
9. [Inspecting a record](#inspecting-a-record)
10. [Selecting and copying](#selecting-and-copying)
11. [Bookmarks and jumping to a record](#bookmarks-and-jumping-to-a-record)
12. [Columns](#columns)
13. [Several files and directories](#several-files-and-directories)
14. [Docker, Kubernetes and other pipes](#docker-kubernetes-and-other-pipes)
15. [Log formats and parsers](#log-formats-and-parsers)
16. [Opening other files, the file picker and the command palette](#opening-other-files-the-file-picker-and-the-command-palette)
17. [Configuration, environment and key bindings](#configuration-environment-and-key-bindings)
18. [Large files and memory](#large-files-and-memory)
19. [Colours, terminals and Windows](#colours-terminals-and-windows)
20. [Generating test logs with logigen](#generating-test-logs-with-logigen)
21. [Troubleshooting](#troubleshooting)
22. [Building from source and releases](#building-from-source-and-releases)
23. [Cheat sheet](#cheat-sheet)

---

## Getting started

**Install.** Download the archive for the platform from the release page, check it against `SHA256SUMS`, unpack it and put `logi` (`logi.exe` on Windows) on the `PATH`. To build from source, see [Building from source and releases](#building-from-source-and-releases).

**Check the installation.**

```bash
logi --version
logi --help
logi --list-parsers
```

**Open a first log.**

```bash
logi application.log
```

The file opens at once, even when it is many gigabytes: records are read from disk as they are needed. Use `↑` `↓` to move, `/` to search, `f` to filter, `?` for help and `q` to quit.

**Three ways to give logi input**

| Input | Command | Mode |
|---|---|---|
| A finished file | `logi app.log` | static: browse |
| A file that is still being written | `logi -f app.log` | live: follow |
| Another program's output | `kubectl logs -f web \| logi` | live: follow the pipe |

The same keys work in every mode.

---

## Reading the screen

An example (the layout adapts to the terminal width and to the log format):

```text
 STATIC  logi  app.log  · generic            ← header: mode, source, parser
 ID   TIME           LEVEL  MESSAGE          ← column header (the selected column is highlighted)
 1    10:00:01.000   INFO   service started  ← records
 2    10:00:02.113   WARN   slow response
 …
 3/300  │  filter: severity:>=warn  │  ? help …             12 KiB      ← status line
```

- **Header.** The mode badge reads `STATIC` for a browsed file, `LIVE` for a followed one and `LIVE ‖ paused` when following is paused. Next to it are the source (file, directory or `stdin`) and the parser in use. In a multi-file view the header also names the file of the current row, in that file's colour.
- **Column header.** Which columns appear depends on the format: record ID, time, level, source, message, and format-specific fields such as `thread` or `logger`. The highlighted column is the target of `x` (hide).
- **Records.** Severity is coloured. The ID is a stable number assigned in arrival order and is what bookmarks, selections and *Jump to record ID* use.
- **Status line.** Left: the position in the current view (`3/300`), the active filter (`…` while it is still being applied, `(capped)` when the result limit was reached) and key hints. Right, in both modes: coloured counters of the records, `C:` for critical and fatal, `E:` for errors and `W:` for warnings (each shown when above zero). In static mode they count the whole file, are filled in by a background scan that starts once the file is indexed (`counting…` is shown until it finishes), and are followed by the file size, or `indexing 40%` while the file is still being indexed. In live mode they count the records currently retained and are followed by `buf 1,200/50,000` (records kept out of the capacity), and `dropped N` once older records have been pushed out. The counters always cover the whole log, not the filtered view. On a narrow terminal the key hints on the left give way first (whole hints at a time; `? help` and `q quit` stay). Short messages such as *copied 3 records* appear here too.

A static file can be browsed while it is still being indexed; the progress is shown in the status line.

---

## Browsing a file

```bash
logi application.log
```

| To | Press |
|---|---|
| Move one record | `↓` `↑` or `j` `k` |
| Move one page | `PgDn` `PgUp`, or `Space` `b`, or `Ctrl+D` `Ctrl+U` |
| Jump to the first / last record | `g` `Home` / `G` `End` |
| Jump to a record by ID | `Ctrl+J`, type the number, `Enter` |
| Scroll with the mouse | wheel (three records a step); click a row to select it |

Static mode reads the file as it was when opened. For a file that keeps growing, use `-f` ([Following a log live](#following-a-log-live)) or reopen it.

**Start with a different parser or without some columns**

```bash
logi --parser json service.jsonl
logi --hide-columns id,source application.log
```

---

## Following a log live

```bash
logi -f application.log
```

logi loads the last 8 MiB (the default `--backlog`) and then shows new records as they arrive, scrolling to keep the newest in view.

**Choose how much history to start with**

```bash
logi -n 500 application.log                  # the last 500 lines (-n, long form --lines, implies -f)
logi -f --backlog 64MiB application.log      # a bigger window of history
logi -f --backlog 0 application.log          # only records written from now on
logi -f --from-start application.log         # the whole file first, then follow
```

`-n` cannot be combined with `--backlog` or `--from-start`: use one way of choosing the history.

**Pause and resume.** Press `p` (or `Ctrl+T`) to pause following. New records keep arriving, but the view stops moving, and the header shows `LIVE ‖ paused`. Press `p` again to resume. Moving up also pauses; moving down to the last record resumes.

**What logi handles for a live file:** growth, truncation (`> app.log`), rotation (the file is renamed and a new one created) and re-creation. A renamed rotation file is not read twice.

**Network drives.** File-system notifications are unreliable on network shares; check the file on a timer instead:

```bash
logi -f --poll //server/share/app.log
```

**Limits.** Live mode keeps a bounded ring of records: 50,000 records and 256 MiB by default. When a limit is reached the oldest records are dropped. See [Large files and memory](#large-files-and-memory) to change them.

---

## Searching

Press `/` (or `Ctrl+F`) and type. The cursor jumps to the first match as the text is typed, and always searches from where the search began.

| Key in the search box | Does |
|---|---|
| `Enter` | Keep the current match and close the box |
| `Esc` | Cancel and return to where the search started |
| `Alt+R` | Switch to regular-expression mode (press again for literal) |
| `Alt+F` | Switch to field mode (press again for literal) |
| `Alt+C` | Toggle case sensitivity |
| `Ctrl+U` / `Ctrl+W` | Clear the box / delete a word |

After closing the box, `n` (or `F3`) goes to the next match and `N` (or `Shift+F3`) to the previous one. A search wraps around at the end of the view.

**Modes**

| Mode | Example text | Finds |
|---|---|---|
| literal (default) | `connection refused` | records containing that text, ignoring case |
| regular expression (`Alt+R`) | `timeout\s+\d+ms` | records matching the RE2 expression |
| field (`Alt+F`) | `thread=43` | records whose field `thread` equals `43` |

The matching text is highlighted in the column where it occurs, including the fields of NCache and JSON records. Search covers the current view: when a filter is active, only the filtered records are searched, and the whole record text, including time and level, is searched.

To start in another mode, use `--search-mode regex` (or `search.mode` in the config file).

Search finds one record at a time; to **show only** matching records, use a filter.

---

## Filtering

Press `f` (or `Ctrl+L`), type a query and press `Enter`. Records that do not match are hidden. The query stays in the status line. Press `c` (or `Ctrl+K`) to clear it. An invalid query is reported in the box and nothing changes. Pressing `f` again shows the current query for editing.

A query is a list of terms separated by spaces. All terms must match (AND). A leading `-` negates a term.

| Term | Matches |
|---|---|
| `severity:error` | exactly that level |
| `severity:>=warn` | that level or worse; also `>`, `<=`, `<` |
| `severity:warn,error` | any of the listed levels |
| `source:api` | the source (file or service) name contains `api` |
| `message:timeout` | the message contains the text |
| `message:"connection refused"` | the message contains the phrase |
| `thread=43` | the structured field `thread` equals `43` (any field name) |
| `re:timeout\s+\d+` | the record text matches the regular expression |
| `since:15m` `until:14:30` | a time window ([Time windows](#time-windows)) |
| `"exact phrase"` | the record text contains the phrase |
| `word` | the record text contains the word |
| `-word` `-severity:debug` | negation of any term |

Text comparisons ignore case. `level:` and `sev:` are accepted for `severity:`, `src:` for `source:`, `msg:` for `message:` and `regex:` for `re:`. Level names are normalised across logging stacks, so `WARNING`, `WRN`, `Warning` and `warn` are the same level.

**Examples to type after pressing `f`**

<!-- filter-examples -->
```text
severity:>=warn
severity:error message:timeout
severity:>=warn -message:"health check"
source:payments severity:>=error
host=server01 -severity:debug
re:order-\d{6} since:1h
```

**The same filters at start**

A query can be given when logi starts, and the common terms have their own flags. The filter is applied before the screen opens, so non-matching records are hidden, not merely highlighted.

```bash
logi --filter 'severity:>=warn message:timeout' app.log
logi --level warn,error app.log
logi --level warn --level error app.log                  # same: repeated levels are ORed
logi --contains "connection refused" --contains retry app.log   # both texts required
logi --regex "timeout|failed" app.log
logi --field host=server01 app.json
logi --exclude "health check" --exclude heartbeat app.log
logi -f app.log --level warn,error --contains "connection refused"
```

`--level` values are ORed together; every other flag adds a condition that must also hold. Flags combine with each other and with `--filter`. Invalid filters are rejected before the screen opens, with a message naming the problem.

Live filters keep up: new records are tested as they arrive. A filter's result list is capped at 1,000,000 matches by default (`performance.max_filter_matches`); the status line shows `(capped)` if the cap is reached, and a narrower filter avoids it.

---

## Time windows

```bash
logi --since 15m app.log                       # the last 15 minutes
logi --since 2h --until 30m app.log            # from two hours back until half an hour back
logi --since 09:00 --until 09:30 app.log       # a time of day
logi --since 2026-10-15 --until 2026-10-15 app.log          # a whole day
logi --since "2026-10-15 14:30" app.log
logi --filter 'since:1h severity:>=error' app.log
```

In the filter box the same terms are `since:15m` and `until:"2026-10-15 14:30"`; quote values that contain a space.

- **Durations** (`15m`, `2h`, `1d`, `1h30m`) are measured back from the **newest record in the log**, not from the clock. The last 15 minutes of an old file are therefore the last 15 minutes *of that file*, and logs without a time zone work too.
- **Absolute times**: `14:30` (on the newest record's date), `2026-10-15`, `2026-10-15 14:30`, or a full timestamp such as `2026-10-15T14:30:00Z`.
- A bare date given to `--until` includes that whole day.
- A time with a zone is compared as an exact instant; a time without one is compared with what the log shows.
- Records without a timestamp (for example stack-trace lines that are not joined to a record) are kept.
- Piped input has no file to measure from, so relative times use the clock.

---

## Finding errors and warnings

| To jump to | Press |
|---|---|
| the next error (error, critical or fatal) | `Alt+E` |
| the previous error | `Alt+Shift+E` |
| the next warning or anything worse | `Alt+Shift+W` |
| the previous warning or worse | `Alt+W` |

The jumps respect the current filter and work on large files without freezing the screen. The status line counters (`C:`, `E:`, `W:`) show how many critical, error and warning records the log holds: the whole file in static mode, the retained records in live mode.

To list only problems: `logi --level warn,error app.log`, or press `f` and type `severity:>=warn`.

---

## Inspecting a record

Press `Enter` (or `i`) on a record to open its detail view.

- A record with several lines (a stack trace) is shown in full.
- Parsed fields are listed by name: for NCache logs the real columns instead of the raw tab-separated line, for JSON the object, pretty-printed.
- `←` `→` (or `[` `]`) move to the previous or next record without closing the view; `↑` `↓`, `PgUp` `PgDn` and the mouse wheel scroll; `g` returns to the top of the record.
- `y` copies the record to the clipboard.
- `Esc` (or `Enter`, `i`, `q`) closes the view.

Records longer than the size limit (64 KiB by default) are shown cut and marked as truncated; change this with `--max-record-size`.

---

## Selecting and copying

To copy several records:

1. Move to the first record and press `v` (the status line says *selection started*).
2. Move to the last record and press `v` again (*selected records 12–40*). The selected range is marked in the ID column.
3. Press `y`.

The raw text of the records goes to the clipboard, in the order shown on screen, one record per line, with terminal colour codes removed. The selection is then cleared. `V` clears a selection without copying.

To copy a single record, press `y` without a selection (or inside the detail view).

With a filter active, a selection covers only the visible records between its two ends.

**Requirements.** logi sends the text to the terminal with an OSC 52 escape sequence, and the terminal places it on the system clipboard. This also works over SSH, because the sequence travels through the connection. Check the terminal's settings or documentation for *OSC 52* or *clipboard access*; some terminals ask for permission the first time.

- **tmux:** add `set -g set-clipboard on` to `~/.tmux.conf`.
- **Size:** one copy is limited to 512 KiB, because many terminals ignore larger payloads. The copy is cut at a record boundary and the status line says so (for example *stopped at the 512.0 KiB clipboard limit*). Copy in several steps for larger ranges.
- **No confirmation:** a terminal that does not support OSC 52 gives no error. If nothing arrives, see [Troubleshooting](#troubleshooting).

---

## Bookmarks and jumping to a record

- `m` bookmarks the current record (or removes the bookmark). A bookmarked record is marked in the gutter with ★.
- `]` jumps to the next bookmark and `[` to the previous one in the current view.
- `Ctrl+J` asks for a record ID and jumps there if the record exists in the current view.

Bookmarks are kept for the session as record IDs, up to 10,000 in live mode, and are cleared when another file is opened.

---

## Columns

The columns shown depend on the parser. Use `h` `←` / `l` `→` (or `Shift+Tab` / `Tab`) to select a column; it is highlighted in the header, and a mouse click on a header selects it too.

| To | Press |
|---|---|
| hide the selected column | `x` |
| show every hidden column | `X` |

At start:

```bash
logi --hide-columns id,thread app.log
logi --show-columns source -f logs/a.log logs/b.log
```

Column names are `id`, `time`, `level`, `source`, `message` plus the fields of the format (NCache cache log: `appdomain`, `logger`, `thread`, `event`; NCache event log: `component`, `eventid`). In the config file they are `columns.hidden` and `columns.shown`.

---

## Several files and directories

### Several files in one view

```bash
logi -f logs/api.log logs/web.log logs/worker.log
logi -f logs\*.log                              # wildcards: logi expands them itself on Windows
```

The files must be in one directory. Records are merged in one view and each file gets its own colour.

### A whole directory

```bash
logi /var/log/myapp/                            # newest matching file, browsed; Ctrl+O picks another
logi -f /var/log/myapp/                         # follow the newest file, rolling over to new ones
logi -f --dir-mode all /var/log/myapp/          # follow every matching file at once
logi -f --dir-mode all --glob '*.log,*.txt' logs/
logi -f --dir-mode all --max-files 64 logs/     # raise the limit of 32 followed files
```

- **`--dir-mode latest`** (the default) follows the newest file and switches when a newer one appears, as with daily log files.
- **`--dir-mode all`** follows every matching file in one merged view.
- **`--glob`** lists the patterns that select files, separated by commas. By default all files are considered except hidden files, sub-directories, compressed archives (`.gz`, `.zip`, `.bz2` …) and numbered rotation leftovers such as `app.log.1`. Giving a pattern replaces the leftover rule, so `--glob 'app.log*'` includes them.
- When a directory is opened **without** `-f`, the newest matching file is opened and `Ctrl+O` lists the files so another can be chosen.

### Reading a merged view

In `--dir-mode all` (and for several files):

- A thin **colour bar** in the gutter shows the file each row came from; the header names the file of the current row.
- The **source** column is hidden to leave room for the message. Show it with `--show-columns source` or press `X`.
- Colours are stable: files present at start are coloured in alphabetical order, later files take the next colour, and a file never changes colour. There are ten colours, none of them red. With more than ten files, `--source-markers` marks repeated colours with a shape (◆ ▲ ■ ●).
- `--source-gutter=off` removes the bar and `--source-gutter=on` always shows it (default `auto`: shown while the source column is hidden).
- Filter to one file with `source:` — for example `source:api`.

---

## Docker, Kubernetes and other pipes

Any command whose output is piped into logi is read as a live stream. The keyboard still works.

```bash
docker logs -f web 2>&1 | logi
docker compose logs -f | logi
kubectl logs -f deploy/web | logi
kubectl logs -f -l app=web --prefix --all-containers | logi
journalctl -f -u myservice | logi
tail -F /var/log/syslog | logi
ssh server tail -f /var/log/app.log | logi
```

`-` also names standard input explicitly (`some-command | logi -`), but piped input is detected automatically.

- **`2>&1`** is needed with `docker logs` because Docker writes a container's stderr to stderr.
- **Compose and kubectl prefixes** (`web-1  |` and `[pod/web-7d9f/web]`) become the record's source: each service or pod gets its own colour, and `source:web-1` filters to it. Stack traces stay with their own service.
- **Timestamps** added by `docker logs -t` or `kubectl logs --timestamps` are recognised, and an extra stamp in front of an application timestamp is removed so multi-line records stay together.
- **Colour codes** from the application are removed so levels and timestamps are found.
- **Saved container logs** can be opened as files: Docker's `json-file` logs (`/var/lib/docker/containers/<id>/<id>-json.log`) and the CRI files under `/var/log/pods` (profile `cri`).
- When the command ends (the container stops, the pod restarts) the view stays open and shows that the input ended. logi does not reconnect: run the command again.
- **PowerShell** can wrap a native command's stderr lines in error records when `2>&1` is used. Run the whole pipeline through cmd instead: `cmd /c "docker logs -f web 2>&1 | logi"`.
- Filters and time windows work on pipes as on files (relative times use the clock, as there is no file to measure from):

```bash
kubectl logs -f deploy/web | logi --level warn,error
docker compose logs -f | logi --exclude healthcheck
```

---

## Log formats and parsers

By default logi detects the format from a sample of the input (`--parser auto`) and falls back to plain text.

```bash
logi --list-parsers
```

| Profile | Use for |
|---|---|
| `generic` | plain text: timestamp and severity are found heuristically |
| `json` | JSON Lines, one object per line (zap, pino, logrus, slog, klog …) |
| `cri` | Kubernetes and containerd container log files |
| `ncache` | NCache cache logs |
| `ncache-events` | NCache event logs |

**Force a parser** when detection picks the wrong one:

```bash
logi --parser json service.jsonl
logi --parser generic server.log
logi --parser ncache cache.log
```

**Multi-line records.** A line that starts with a timestamp starts a new record; following lines without one (a stack trace) belong to it. Limits: `--max-record-size` (default 64 KiB) and `--max-lines` (default 2000); a longer record is marked truncated. To treat every physical line as its own record:

```bash
logi --no-multiline app.log
logi --max-lines 500 --max-record-size 16KiB app.log
```

**JSON logs.** Fields become columns and can be used in filters (`--field level=error`, `host=server01`) and in field search (`Alt+F`). The detail view pretty-prints the object.

**NCache.** The NCache plugin is built in and on by default (it provides both `ncache` and `ncache-events`). logi reads the real header widths, validates rows against them, realigns imperfectly padded exports, handles `END` terminators and folds stack traces into their record. To switch it off: `logi --disable-plugin ncache file.log`; to switch a plugin back on when the config file disabled it: `logi --enable-plugin ncache file.log`. Both options can be repeated or given a comma-separated list.

**Terminal safety.** Terminal escape sequences inside log text are removed, so a log cannot move the cursor, clear the screen or run terminal commands.

---

## Opening other files, the file picker and the command palette

- **Open a file:** press `o` (or `Ctrl+P`), type a path and press `Enter`. The new file opens with the same options; the filter, bookmarks and selection are reset.
- **Pick from the directory:** when a directory was opened, `Ctrl+O` lists its files; type to narrow the list, `↑` `↓` to choose, `Enter` to open.
- **Command palette:** press `:` (or `F1`, `Ctrl+Shift+P`), type part of a command name, choose with `↑` `↓` and press `Enter`. Every action is available there, including those without an easy key.
- **Help:** `?` lists all keys and commands with their current bindings, including any rebound ones.

---

## Configuration, environment and key bindings

Settings are resolved in this order; later ones win:

```text
built-in defaults  →  config file  →  LOGI_* environment variables  →  command-line flags
```

### Config file

The file is looked for at `--config <path>`, then `$LOGI_CONFIG`, then `<user-config-dir>/logi/config.yaml` (`~/.config/logi/config.yaml` on Linux, `~/Library/Application Support/logi/config.yaml` on macOS, `%AppData%\logi\config.yaml` on Windows). logi only reads it. A bad key or value is reported at start with the key's name, and unknown keys are rejected so that typos are not silent.

```yaml
color: auto
parser: auto

buffer:
  capacity: 100000       # live mode: records kept
  max_bytes: 512MiB

live:
  backlog: 16MiB

limits:
  max_record_size: 128KiB

search:
  mode: regex

columns:
  hidden: "id"

plugins:
  ncache:
    enabled: false
```

[`config.example.yaml`](config.example.yaml) lists every key with its default and meaning. Use a team file with:

```bash
logi --config ./team-logi.yaml app.log
```

### Environment variables

| Variable | Sets |
|---|---|
| `LOGI_CONFIG` | path of the config file |
| `LOGI_FOLLOW` | `true` to follow by default |
| `LOGI_PARSER` | default parser |
| `LOGI_COLOR` | `auto`, `always` or `never` |
| `LOGI_BUFFER` | live buffer size in records |
| `LOGI_DISABLE_PLUGINS` | plugins to disable, e.g. `ncache` |
| `NO_COLOR` | any value turns colours off |

```bash
export LOGI_COLOR=never
LOGI_FOLLOW=true logi app.log
logi --follow=false app.log        # a flag overrides the environment; booleans take =false
```

### Rebinding keys

Under `keys:` in the config file, give an action and a comma-separated list of keys. The list **replaces** the default keys of that action, and a key that another action used before moves to the new one.

```yaml
keys:
  search: "ctrl+f, /"
  copy: "y, ctrl+y"
  quit: "q"
```

Actions: `down`, `up`, `page_down`, `page_up`, `top`, `bottom`, `search`, `search_next`, `search_prev`, `next_error`, `previous_error`, `next_warning`, `previous_warning`, `jump_record`, `toggle_bookmark`, `next_bookmark`, `previous_bookmark`, `select_range`, `clear_selection`, `copy`, `filter`, `clear_filter`, `inspect`, `follow`, `quick_open`, `palette`, `help`, `close`, `quit`, `column_prev`, `column_next`, `hide_column`, `show_columns`, `pick_file`. Press `?` to check the result.

---

## Large files and memory

**Static files** use little memory regardless of size: a small block index (built in the background) and a small cache. Searches and filters over a large file reject non-matching records on their raw text and use several CPU cores; results still arrive in order.

**Live mode** is bounded by records and by bytes:

```bash
logi -f --buffer 200000 app.log              # keep up to 200,000 records (default 50,000)
logi -f --buffer-bytes 1GiB app.log          # byte budget (default 256 MiB; 0 = unlimited)
logi -f --backlog 32MiB app.log              # initial history (default 8 MiB)
```

Whichever limit is reached first drops the oldest records. A very busy log with long records is limited by bytes; a log of short lines by record count.

**Tips**

- To see *older* data than the live buffer holds, open the file without `-f` (static mode reads all of it) or raise `--buffer` and `--backlog`.
- Narrow with `--level`, `--since` or `--exclude` when only some records matter; hidden records still count against the buffer, so raise `--buffer` for rare matches in a very noisy log.
- Very long lines are cut at `--max-record-size`; very long stack traces at `--max-lines`.
- In static mode the severity counters come from one extra background pass over the file (about a quarter of a second per million records on one core), using spare cores and no extra memory. To skip it for a huge file, set `performance.static_counts: false` in the config file; the counters then appear in live mode only.
- `--poll` trades a little latency for reliability on network shares; `performance.batch_interval` (default 25 ms) controls how often the screen updates under heavy input.

---

## Colours, terminals and Windows

- **Colours** are detected automatically. `--color always` forces 16-colour output, `--color never` or the `NO_COLOR` variable turns colours off. logi uses the terminal's own palette, so it follows the theme.
- **Terminal size.** The layout adapts to the window; widen it to see more columns, or hide columns with `x`.
- **Mouse.** The wheel scrolls and clicks select a row or a column header. Terminals usually offer a modifier key (often `Shift`) to select text with the mouse.
- **Windows wildcards.** cmd.exe and PowerShell do not expand `*.log`; logi does, so `logi -f logs\*.log` works.
- **Windows pipes.** `type app.log | logi` and `Get-Content app.log -Wait | logi` work. For `docker`/`kubectl` with `2>&1` use `cmd /c "… | logi"` (see [Docker, Kubernetes and other pipes](#docker-kubernetes-and-other-pipes)).
- **Windows files.** logi opens log files so that they can still be rotated or deleted by the application writing them.
- **Windows Terminal, PowerShell and cmd** all work; the clipboard feature needs a terminal that supports OSC 52.

---

## Generating test logs with logigen

`logigen` writes synthetic logs for trying logi out and for measuring it.

```bash
logigen -n 100000 -o sample.log                       # 100,000 text records
logigen -n 1000000 -format json -o big.jsonl          # JSON Lines
logigen -n 50000 -format ncache -o ncache.log         # NCache-style
logigen -n 20000 -multiline 0 -o oneline.log          # no stack traces (they follow ERROR records by default)
logigen -n 1000 -numbered -o numbered.log             # end every message with "(item N)" to tell records apart
logigen -n 5000 -seed 7 -o repeatable.log             # the same seed gives the same file
logigen -live -rate 2000 -o live.log                  # keep appending 2000 records a second
logigen -live -rate 500 -rotate 10000 -o rotating.log # rotate the file every 10,000 records
logigen -dir logs -files 5 -n 20000                   # five date-named files in logs/
logigen -live -dir logs -roll-every 5000              # live: start the next day's file every N records
```

Then:

```bash
logi sample.log
logi -f live.log
logi -f --dir-mode all logs/
```

---

## Troubleshooting

**Nothing is copied when `y` is pressed.** The terminal must support OSC 52 and allow it. Under tmux, set `set -g set-clipboard on`. Some terminals have a setting such as "allow applications to access the clipboard". Copies over 512 KiB are cut; the status line says so.

**No colours.** `NO_COLOR` or `LOGI_COLOR=never` is set, the terminal does not report colour support, or output is not a terminal. Try `--color always`.

**A file opened without `-f` does not show new lines.** Without `-f` the file is a snapshot. Use `logi -f file`.

**Piping shows nothing, or only part of the output.** Programs such as `docker logs` write the container's stderr to stderr; add `2>&1`. If the command ended, the view stays open and says so.

**Levels, timestamps or stack traces are wrong.** Check the parser in the header. Force one with `--parser generic` or `--parser json`, and use `--no-multiline` if lines wrongly join. If records are cut, raise `--max-record-size` and `--max-lines`.

**A filter is rejected.** The message names the problem: for example an unknown severity, an unbalanced quote, or an invalid regular expression. Severities are `trace`, `debug`, `info`, `notice`, `warn`, `error`, `crit` and `fatal` (and common spellings of them).

**`--since 15m` shows nothing.** The window is measured back from the newest record in the log; check the log has timestamps (the header shows the parser) and that the newest record is what is expected. For piped input, relative times use the clock.

**Wildcards report "no files match".** The pattern matched nothing in that directory. Check the path; on Unix quote patterns for `--glob` (`'*.log'`).

**Live view is behind or missing records.** Raise `--buffer` and `--buffer-bytes` if the log is very busy; use `--poll` on network drives.

**Settings are ignored.** Remember the order: flags beat environment variables, which beat the config file. `logi --help` prints the config file path in use.

**A config file error at start.** The message names the key and the problem. Unknown keys are rejected on purpose.

**Exit codes.** `0` success, `1` a runtime failure (a file or session could not be opened), `2` a usage or configuration mistake. Scripts should test the code, not the message.

---

## Building from source and releases

```bash
go test ./...                       # run the tests
go build -trimpath -ldflags "-s -w -X main.version=v1.0.0" -o logi ./cmd/logi
make build                          # the same for the current platform
make dist                           # cross-compile the release targets
bash scripts/release.sh 1.0.0       # build the archives and SHA256SUMS into dist/
```

On Windows, build for Windows or for another system from the same machine:

```bash
go build -trimpath -ldflags "-s -w -X main.version=v1.0.0" -o ..\dist\logi.exe .\cmd\logi
```

To build for another platform, set `GOOS` and `GOARCH` (for example `linux`/`amd64`, `linux`/`arm64`, `darwin`/`arm64`) and `CGO_ENABLED=0`; no other toolchain is needed. In PowerShell:

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
go build -trimpath -ldflags "-s -w -X main.version=v1.0.0" -o ..\dist\logi-linux-amd64 .\cmd\logi
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

**Automatic builds on GitHub.** Two workflows in `.github/workflows` produce the binaries:

- **Every push and pull request** (`ci.yml`) runs the checks and the tests on Linux, Windows and macOS, then builds six archives (Linux, macOS and Windows, each for amd64 and arm64) and starts the Linux, macOS (arm64) and Windows (amd64) builds to confirm they run. The archives are kept for 30 days under **Actions → the run → Artifacts**.
- **A version tag** (`release.yml`) repeats the checks, builds the same six archives with the version embedded, and publishes them with `SHA256SUMS` and the changelog entry as a GitHub release.

To publish a release, rename the `## Unreleased` heading in `CHANGELOG.md` to the version (for example `## 1.1.0 — 2026-11-01`), commit, and push a tag:

```bash
git tag v1.1.0
git push origin v1.1.0
```

A tag such as `v1.2.0-rc.1` publishes a pre-release. The release can also be built by hand without publishing: **Actions → Release → Run workflow**. Compatibility rules are in [`STABILITY.md`](STABILITY.md).

---

## Cheat sheet

**Start**

```bash
logi app.log                          # browse
logi -f app.log                       # follow
logi -n 300 app.log                   # follow, last 300 lines
logi -f dir/                          # newest file in a directory
logi -f a.log b.log                   # several files, merged
cmd | logi                            # a pipe
```

**Narrow at start**

```bash
logi --level warn,error app.log
logi --since 30m app.log
logi --contains timeout --exclude healthcheck app.log
logi --field host=server01 app.json
logi --filter 'severity:>=warn -source:probe' app.log
```

**Keys**

| | |
|---|---|
| move | `↑↓` `jk` · `PgUp/PgDn` · `g` `G` |
| search | `/` · `n` `N` · `Alt+R` regex · `Alt+F` field · `Alt+C` case |
| filter | `f` · `c` clear |
| errors / warnings | `Alt+E` `Alt+Shift+E` · `Alt+Shift+W` `Alt+W` |
| inspect | `Enter` · `←→` previous / next · `y` copy · `Esc` |
| select + copy | `v` start · `v` end · `y` · `V` clear |
| bookmarks | `m` · `[` `]` |
| jump to ID | `Ctrl+J` |
| live | `p` pause / resume |
| columns | `h` `l` select · `x` hide · `X` all |
| files | `o` open · `Ctrl+O` directory picker |
| commands | `:` palette · `?` help · `q` quit |

**Filter terms**

<!-- filter-examples -->
```text
severity:>=warn   severity:warn,error   source:api   message:"timed out"
thread=43   re:order-\d+   since:15m   until:14:30   "exact phrase"   -word
```
