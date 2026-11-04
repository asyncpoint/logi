# Changelog

## Unreleased

- Internal: command-line flags are a table (`flagDefs` in `internal/config/flags.go`) with aliases declared once per flag; `applyFlags` is split into parsing, the cross-flag rules and a table of startup-filter term sources (`startupTerms`). Checked against the previous implementation on 15,000 random command lines (config, errors and `--help` output all identical).
- Opening a static file now uses one file handle for the type check, the sample that picks the parser, and the store (`StaticOptions.File`), instead of opening it twice. The sample and the index can no longer come from two different files if the file is rotated in between.
- Internal: config-file loading (`applyMap`) is a table of dotted key → setter (`fileSettings` in `internal/config/file.go`); adding a file option is one line. A test checks it against the previous implementation on 20,000 random files. One deliberate difference: when several plugins have invalid `enabled` values, the first error reported is now always the same (plugins are visited in name order) instead of varying from run to run.
- Docs: `docs/config.example.yaml` now lists every rebindable action (it was missing `copy`, selection, bookmark and error/warning navigation actions), and tests keep it in step with the code.
- Internal: opening a session is split by input kind (stdin, live file, live directory, static directory, static file) into one small opener each in `internal/app/session_open.go`; `detectKind` is the single place that chooses the mode. `NewSession` no longer branches through all five inline.
- Internal: the static scan code passes a `scanSpec`, an `indexView` and a `segment` instead of nine loose parameters; behaviour and speed are unchanged.
- Fixed: `Mode.String()` (session and search) no longer panics on an out-of-range value; the static sample read uses the platform file opener like the rest of the file access.
- Internal: the 950-line `internal/config/config.go` is split by responsibility into `config.go`, `validate.go`, `values.go`, `env.go`, `flags.go`, `flagerrors.go`, `paths.go` and `file.go`. Declarations were moved unchanged; behaviour is identical.
- Static mode now shows the `C:` `E:` `W:` severity counters in the status bar, as live mode does. A background scan over the indexed file counts the records by severity (in parallel, retaining nothing; about 0.25 s per million records on one core) and the bar shows `counting…` until it is done. It can be turned off with `performance.static_counts: false`. The key hints on the left of the status bar now drop whole hints on a narrow terminal, keeping `? help` and `q quit`, instead of being cut in half.
- `logigen` no longer ends every generated message with `(item N)`, which made the sample logs look artificial (logi shows messages exactly as the file has them, so it appeared in the detail view). The suffix is available as `-numbered`, and with that flag the output is byte-identical to before. The shipped `test/fixtures/sample-app.log` and `sample-ncache.log` are regenerated without it.
- Fixed: the detail view of a multi-line record (for example an NCache error with a stack trace) ran every line of its `message` together on one row. Line breaks are now kept, so the exception and each stack frame appear on their own rows, as in the log file. The cause was `SanitizeKeepTabs`, which treated the newline as an unsafe control character.
- Tooling: GitHub Actions now builds the six release archives (Linux, macOS and Windows for amd64 and arm64) on every push and runs them on Linux, macOS and Windows; a pushed `vX.Y.Z` tag publishes them as a release with `SHA256SUMS` and the changelog entry, and refuses a tag whose version has no changelog section. The release script is called through `bash` so it does not depend on a file mode, ships `docs/` in each archive, and the workflows, analysis tools and `.gitignore` are tidied.
- Docs: `README.md` is rewritten to describe logi itself (no first person) with a common-tasks section; the new `docs/HOWTO.md` is a task-by-task guide to every feature (screen, browsing, following, search, filters, time windows, selecting and copying, bookmarks, columns, directories, pipes, formats, configuration, tuning, troubleshooting, `logigen`). `CONTRIBUTING.md`, the technical docs, the ADRs and older changelog entries are reworded the same way, and `CONTRIBUTING.md` now names the real `make dist` target (it said `make cross`). Tests keep the documents honest: every command example must parse, every option and default key must be documented, internal links must resolve, and the text must stay impersonal. `--glob` and archives are now described accurately (compressed archives are never opened).
- Added: `y` copies the selected records, or the current record, to the clipboard (OSC 52), from the record list and the detail view. Raw text, current view order, colour codes removed, capped at 512 KiB.
- Fixed: a line that begins with terminal colour codes (coloured timestamps from docker, kubectl or coloured loggers) is now recognised as the start of a new record. Before, it was merged into the previous record.
- Fixed: searching inside a filtered view of a static file matched only the message text, so level and timestamp text (`WARN`, a date) were never found. It now matches the whole record, like an unfiltered search.
- Piped input no longer allocates a 256 KiB buffer per read (about 66x the input size for small reads, now about 1x); buffers are recycled.
- Stripping terminal colour codes is about 2x faster on coloured logs.
- Filter and search no longer evaluate the predicate twice per match; the raw prefilter is derived once per filter generation.
- Backward search over large static files sizes its windows by bytes so they can use the parallel scan.
- Quitting during a very large backlog read (`--backlog -1`) is prompt; the read stops at the next chunk.
- Directory listing is cheaper (no per-file path resolution for regular files) and the newest-file lookup for `since:` is cached for one second.
- Tooling: CI and documentation now follow the Go version declared in `go.mod` (they said 1.23.2 / 1.22 while `go.mod` requires 1.25.0).

## 1.0.0 — 2026-10-23

The first stable release. From here on the command line, configuration file, filter language and exit codes follow [semantic versioning](docs/STABILITY.md). Compared with 0.4.0 it adds a much faster search and filter path, multi-file and piped (Docker/Kubernetes) viewing, time windows, and a friendlier command line:

- Release tooling: `scripts/release.sh` failed under `set -u` (a variable assigned in the same `local` statement) and wrote the Linux and macOS archives outside `dist/`, so a tagged release would not have produced its artifacts. Both fixed; it now requires an explicit version, and the whole script was exercised end to end for all six targets (archives, `SHA256SUMS`, version stamp). CI pins a staticcheck release that supports the Go 1.23 toolchain.
- Docker and Kubernetes: piped `docker compose logs`, `kubectl logs --prefix` and `docker logs -t` / `kubectl logs --timestamps` output is recognised. The decoration (`web-1  |`, `[pod/x/y]`, the extra Docker timestamp) is removed, each service or pod becomes a source with its own colour (the multi-file colour bar, header name and `source:` filter all work), and multi-line records are framed per source so interleaved stack traces stay together. Detection is conservative (a bracketed timestamp, a table or an ordinary ISO-timestamp log is left alone) and the result does not depend on how the stream is cut into reads.
- Terminal colour codes are stripped from log text at ingestion (files and streams) and in the display sanitizer, so coloured logs get their timestamps and levels parsed instead of showing `[32m` noise; the search prefilter sees the same cleaned text, so searches still match.
- Docker's `json-file` lines (`{"log":…,"stream":…,"time":…}`) show the application's text with its level read from it; a new `cri` profile parses the container-runtime files under `/var/log/pods` (`<time> stdout|stderr F|P <line>`).

- Command line: `--help` is now grouped (Input, Live view, Directories, Filtering, Display, Parsing and limits, Other) with value placeholders, readable defaults (`8 MiB`, not `8388608`), the environment variables, the config file path and recipes; a test fails if a flag is not documented in it.
- New `-n/--lines N` (follow, starting with the last N lines of each file), `--since`/`--until` (and `since:`/`until:` filter terms; durations count back from the newest record in the log, absolute times without a zone are compared with the log's own clock), `-V`, several files in one directory (`logi -f a.log b.log`, merged), wildcard expansion for shells that do not expand them (Windows), and standard input (`-` or piped). With piped input the terminal is made the process's standard input before the interface starts, so keys and mouse work exactly as in a normal launch; the pipe is read separately, and opening a file from the picker while in pipe mode opens that file.
- Friendlier errors: choices such as `--dir-mode` and `--color` ignore case and suggest the nearest value (`did you mean "all"?`), numbers and sizes say what is wrong (`invalid value "lots" for --buffer: not a valid number`), a missing flag value names the flag, one-letter flags no longer get nonsense suggestions, and a missing file reads `cannot find "x" (check the path)` instead of `stat x: ...`.

- Fix JSON message decoding: `\/` and UTF-16 surrogate pairs (`\ud83d\ude00`) are now decoded per RFC 8259 instead of shown literally.
- Harden terminal output: a shared `engine.SanitizeText` now strips C0/C1 controls, bidi overrides and line separators and always yields valid UTF-8; it is applied to file names and source IDs as well as log text.
- Speed up static search and filtering 2–5x by rejecting records on their raw text before copying and parsing them (`engine.RawRanger`); results are identical.
- Faster case-insensitive substring search (anchors on the rarest needle byte and uses `IndexByte`).
- Filtered views no longer re-read and re-parse blocks on every redraw (static block cache 8 -> 256 blocks, still byte-bounded).
- Tailer no longer allocates a ~112 KiB batch per flush on quiet logs; batch buffers are recycled.
- Multiline records that hit the size/line cap now keep a clean prefix instead of silently dropping a middle line.
- Static `Range` scans segments in parallel on multi-core machines (ordered delivery on the caller's goroutine, bounded look-ahead, prompt cancel); single-core machines keep the sequential path. `StaticOptions.ScanWorkers` overrides.
- Year-less timestamps (syslog, glog) now take their year from the file's modification time, with December-in-January rollover, instead of the year the process started in.
- Timestamps with non-whole-hour UTC offsets no longer allocate a time zone per record.
- Tailer detects a file rewritten in place (copytruncate that regrows past the read offset between polls) via a head fingerprint, and re-reads it instead of losing lines.
- `NewSession` no longer returns a half-built session together with an error (it is closed first); removed a dead import-keeper and a misplaced doc comment.
- `LogRecord.Timestamp` is now the compact, pointer-free `engine.Timestamp` (same `IsZero`/`Format`/`Year`… methods): the record shrinks from 152 to 144 bytes and a 1M-record ring GCs about 18% faster.
- The detail view clips a single record read at 4 MiB (was 16) with a visible "record continues" marker and no longer copies the bytes twice.
- `Session` is split by concern (`session_view.go`, `session_filter.go`, `session_search.go`; `session.go` 791 -> 362 lines) and the four near-identical scan loops in `scanRows` are unified.
- Scans now evaluate the parsed-record predicate on the worker goroutines (`engine.MatchRanger.RangeMatching`, replacing `RawRanger`), so parse-bound filters scale across cores while only matching records are buffered; scans that would deliver every record stay sequential.
- Multi-file view (`--dir-mode all`): the source column now has a distinct colour per file (stable for the session, alphabetical for files present at startup, no red, terminal-palette colours) instead of the severity colour, so interleaved files are easy to tell apart. Opt-in `--source-markers` (config `directory.source_markers`, default off): when more than ten files are followed, files whose colour repeats also get a shape marker (◆ files 11-20, ▲ 21-30, ■, ●) in two reserved cells of the source column; without the flag, or with ten files or fewer, the column is unchanged. New `Session.SourceSlot` / `FollowedFileCount` and `DirFollower.Count` support this.
- Multi-file view: the header line now names, in its colour, the file of the row under the cursor (full name, next to the parser name). Behaviour change: the source column is now hidden by default (its width goes to the message; bring it back with `--show-columns source` or `X`), and a file-colour bar in the bookmark cell of the row gutter takes its place (`--source-gutter=auto|on|off`, config `directory.source_gutter`, default auto = shown while the source column is hidden). In a mixed multi-format view the layout is now the generic columns plus each format's `Mixed` columns (`parser.MixedColumns`): for NCache cache logs that adds the `event` column in the source column's place. Bookmarked rows show their star there in the file's colour, and with `--source-markers` rows of repeated colours show their shape there.
- Case-insensitive literal search and filter terms are now Unicode-aware: `école` matches `ÉCOLE`, `σας` matches `ΣΑΣ` (Unicode simple case folding, the same rule as regexp `(?i)`). Highlighting works for non-ASCII matches too (it previously gave up). ASCII queries keep the allocation-free fast path; the Unicode path is allocation-free as well (~1.4 GB/s on a miss). Limits: folding is rune-to-rune (`ß` does not match `SS`), and an ASCII query does not match the Kelvin sign / long s.
- Parallel scans recycle their per-segment result slices through a store-level pool: delivering most records through the parallel path used to allocate ~10x more than a sequential scan (215 MB vs 20 MB per 25 MB file) and ran 2.3x slower on one CPU; it now matches sequential speed there and allocates ~26 MB.
- Fix: a cancelled search/filter over a large static file could keep scanning (and delay shutdown) when no record reached the callback; the scan now takes a context and checks it every 64 records, and parallel workers stop as soon as the consumer does. Covered by a regression test.
- Fuzz targets for every parser of untrusted input (JSON, generic, NCache, framer, filter and search prefilters) with `make fuzz` and a CI job; the prefilter targets enforce that the raw prefilter never rejects a matching record.
- Tests for the `platform` package (file identity, rotation, `FILE_SHARE_DELETE` on Windows); benchmarks for static scan, parallel scan, scattered redraw and the sanitizer.
- `SanitizeText` has an ASCII fast path (about 4x faster on clean text).
- CI: `go mod tidy -diff`, gofmt, staticcheck, govulncheck, Windows/macOS vet, short fuzz runs; release no longer rewrites go.mod.

## 0.4.0

- Add next-error, previous-warning, and jump-to-record-ID navigation.
- Add bookmarks and range selection, with independent row indicators and selection based on filtered-view order.
- Harden search navigation for stale results, filtered-out records, file changes, and live-buffer eviction.
- Improve regression coverage for navigation, search lifecycle, and ANSI-styled help.
- Align CLI help examples and wrap long commands for consistent visual scanning.


## 0.3.0

- Added ergonomic startup filter flags (`--level`, `--contains`, `--regex`, `--field`, and `--exclude`) and composed them with the existing `--filter` query before launching the TUI. Startup filters hide nonmatching records in static, file-follow, and directory-follow views; severity values are ORed within the level category and filter terms are ANDed across categories.

This release consolidates the recent record-storage and query-architecture improvements.

- Used compact `Fields` slices and retained-memory estimates to reduce per-record overhead and keep live retention byte-aware.
- Avoided retaining duplicate static raw payloads in cached records; original bytes are read lazily from the source file.
- Kept filtered rows as compact `uint64` record IDs and materialized `SourcePosition` only when callers need a stable locator, avoiding per-match source-string and offset overhead.
- Made search traversal return the source position captured from the matching record, avoiding a second store lookup after a match is found.
- Validated asynchronous search positions before selecting matches, so evicted live records are reported as expired.
- Added source-position resolution coverage and synchronized architecture documents with the current implementation.
- Kept the existing TUI behavior, bounded live buffer, static offset-based browsing, and NCache support intact.
- Grouped filtered-row index state in `filteredView` and search cancellation/generation/matcher state in `searchState`, reducing unrelated mutable fields on `Session` without introducing a larger controller.
- Added focused tests for query supersession, generation advancement, active matcher state, and cancellation.
- Moved filter-index mutation into `filteredView` methods: query replacement, scan cursors, generation-checked batch commits, match-cap enforcement, eviction trimming, and progress snapshots. `Session` now coordinates the worker and source lifecycle without directly manipulating the index.
- Added regression coverage for stale filter batches, match caps, row lookup, and live-record eviction trimming.
- Kept live filtering active after its view cap is reached, retaining the newest matches rather than leaving the view stale.
- Reduced live filter batches to 256 records to shorten ring read-lock windows during expensive predicates.
- Used a circular ID buffer for capped live filter results, avoiding O(number of matches) shifts on every new batch.
- Removed per-hit allocation from static-cache LRU promotion and prevented duplicate cache entries when concurrent requests load the same block.
- Avoided allocations for common ASCII case-insensitive structured-field comparisons.
- Reused bounded scratch buffers across static range scans and cache misses, avoiding repeated multi-megabyte allocations while browsing or filtering large files.
- Cleared reusable parsed-record slices after appending to the ring so evicted payloads are not accidentally retained by a batch buffer.
- Sized subsequent tailer batches to their actual batch threshold and released parser-detection samples after parser construction.
- Reused coalescing timers in the file tailer and UI event emitter, avoiding a new timer allocation for every filesystem/event burst.
- Bounded the static parsed-block cache by both block count and estimated retained bytes; a block larger than the byte budget can still be read but is not retained.
- Added a reproducible benchmark baseline for record append, parser, framing, and literal-search hot paths.
- Streamed static range reads through a reusable buffer capped at 2 MiB, preventing a large checkpoint block from forcing an equally large temporary read allocation.
- Cached visible TUI column indices until the layout changes and calculated fixed column width once per rendered batch rather than repeatedly for every row.
- Verified the detected column layout as well as the profile name, and refresh it before rendering, so live parser detection cannot leave generic columns beside an NCache status header.
- Corrected the ANSI regression test to distinguish valid ESC-prefixed styling from literal, leaked SGR fragments.
- Lowered the declared Go version to 1.23.2 to match the available validation toolchain; dependency-heavy packages still require their external modules to be available.


## 0.2.2

Synchronized the release notes, CLI documentation, architecture documents, and source comments with the implementation shipped in 0.2.2.

- Restored the structured detail overlay: non-JSON records are rendered as a field/value table using the detected parser column names.
- NCache detail inspection now shows its known columns and parser-derived fields instead of the raw log line.
- JSON records continue to use pretty-printed JSON, including nested objects and arrays.
- Parser-derived fields not present in the display layout are appended in deterministic order.
- Moved normal-view keyboard shortcut hints into the status bar so the log table gets the full vertical space.
- Fixed ANSI/SGR leakage in the detail header severity label.
- Added a regression test covering NCache tabular detail rendering.
- Synchronized the public CLI vocabulary with the implementation: `--follow`, `--buffer`, `--max-record-size`, `--no-multiline`, and explicit plugin enable/disable flags.
- Migrated the incremental filter index from bare IDs to payload-free source positions, including static byte locators and live ring identities.
- Attached source positions to asynchronous search results and made the TUI validate a result before selecting it, so a match evicted from the live ring is reported as expired.
- Added static source-position tests for successful resolution and rejection of mismatched source IDs or offsets.
- Updated the technical design, implementation baseline, and architecture decisions to describe the 0.2.2 implementation rather than the older 0.2.0 baseline.

## 0.2.1

The compact live status bar now reports retained-record severity counts as:

```text
C:2  E:65  W:12  buf 1,104/50,000
```

- `C` combines `CRITICAL` and `FATAL` records to keep the status bar compact without hiding the highest-severity records.
- `E` reports retained `ERROR` records.
- `W` reports retained `WARN` records.
- `buf` continues to report current retained records versus the configured buffer capacity.
- The counters are based on the current bounded buffer, so they decrease when records are evicted.

## 0.2.0

This release combines the strongest parts of the original `logi` implementation with the richer second implementation.

### Added

- Added lazy static-file browsing for very large logs.
- Added directory following with newest-file and all-files modes.
- Added rotation, truncation and file-identity handling.
- Added bounded live retention by record count and approximate bytes.
- Added multiline framing limits.
- Added literal, RE2 regex and structured-field search.
- Added field-aware filtering and match highlighting.
- Added richer column management and a command palette.
- Expanded the NCache parser with adaptive layout detection and event-log support.
- Added a synthetic `logigen` workload generator.
- Added integration and performance fixtures.
- Retained terminal-output sanitization from the earlier release.

### Changed

- Moved the modern TUI to Bubble Tea v2 and Lip Gloss v2.
- Expanded the CLI while retaining GNU-style `--long-options`.
- Kept the NCache parser compiled in and enabled by default.
- Retained bounded-memory behavior as a core design constraint rather than an optional optimization.

## 0.1.0

First release, with live/static viewing, generic and NCache parsing, bounded retention, structured detail overlays, configuration, completion scripts and cross-platform release automation.


### CLI usability fixes (0.3.0)

- Restored the registered flag descriptions in `logi --help`; the options section was empty because the flag set output was discarded.
- Added `--live` as a compatibility alias for canonical `--follow`, retaining `-f`.
- Added tests for all follow aliases and for key options appearing in help output.
