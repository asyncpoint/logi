# Architecture Decision Records

These records describe the architecture shipped by logi 1.0.0. The records are kept focused on decisions that are still true in the current source tree; source comments do not reference undocumented ADR numbers.

## ADR-001 — Separate static and live storage

logi uses lazy offset-based storage for static files and a bounded ring for live data. Static navigation therefore does not require retaining an entire multi-gigabyte file, while live memory remains predictable.

## ADR-002 — Keep the static index in memory and derived from the source file

Static logical-record checkpoints are built progressively when a file is opened. The checkpoints contain offsets/count information rather than retained record payloads. No index is persisted to disk by default.

## ADR-003 — Bound live retention by both records and bytes

A record-count-only limit is insufficient when individual records can be large. The live ring therefore has independent record and approximate-byte limits.

## ADR-004 — Use incremental framing before parsing

Input bytes are first converted into logical records. Parsing then operates on those records. This lets generic, JSON, and specialized profiles share the same input pipeline and allows multiline limits to be enforced before parser-specific work.

## ADR-005 — Use a registry of parser profiles

The registry owns format profiles and detection. `generic` and `json` are core profiles; built-in plugins can register additional profiles. `auto` selects a specialized profile by confidence and falls back to `generic`.

## ADR-006 — Treat NCache as a built-in plugin with multiple profiles

The NCache plugin is compiled into logi and enabled by default. It registers separate `ncache` and `ncache-events` profiles while remaining one configurable plugin.

## ADR-007 — Keep plugins compiled in

The plugin interface is an architectural extension point, not a dynamic module loader. This keeps startup, deployment, and failure behavior predictable.

## ADR-008 — Keep Session as the application coordinator

`Session` coordinates input lifecycle, parser selection, workers, record access, and source status. Opening is split by input kind (stdin, live file, live directory, static directory, static file) into one small opener each in `session_open.go`, and `detectKind` is the single place that chooses the mode. The mutable filter index is grouped in `filteredView`; search generation, cancellation, and matcher ownership are grouped in `searchState`. The engine and input packages do not depend on the TUI.

## ADR-009 — Separate search from filtering

Search finds a matching record for navigation; filtering constructs a persistent view predicate. Neither operation mutates the underlying record store.

## ADR-010 — Use action-based TUI commands

Keyboard bindings resolve to named actions. The same actions can therefore be invoked by the command palette and other UI surfaces without duplicating behavior in key handlers.

## ADR-011 — Use Bubble Tea v2 and Lip Gloss v2

The goal is a modern terminal UI while retaining a terminal-first, keyboard-efficient interaction model.

## ADR-012 — Treat log content as untrusted terminal data

Rendered log content is sanitized before reaching the terminal so control sequences from input cannot manipulate the terminal UI.

## ADR-013 — Keep configuration small and dependency-light

The configuration reader intentionally supports the repository's small YAML subset without adding a general-purpose configuration dependency. CLI values override environment values, which override the configuration file, which overrides defaults.

## ADR-014 — Cap pathological records

Logical record size and multiline physical-line count are bounded. Truncation is explicit while source offsets remain usable.

## ADR-015 — Preserve source data lazily for static inspection

When a user opens the detail view or requests raw content from a static file, the original bytes can be read from disk on demand instead of being retained for every record.

## ADR-016 — Reject records on their raw text before parsing them

A search or filter can name a necessary condition on a record's raw text (a literal or a regular expression). A store that implements `MatchRanger` applies it to the framed bytes and never copies or parses a record it rejects; the full predicate still decides the survivors, so results are identical. The prefilter must see the same text the matcher sees (terminal escapes are stripped first), and a property fuzz test enforces that it never rejects a match.

## ADR-017 — Scan segments in parallel but deliver in order

Static scans are cut into read segments that independent workers parse and test; a single consumer hands records to the caller in file order, on the caller's goroutine, with a bounded look-ahead. Parallelism is used only when results are sparse (a prefilter or predicate is given): delivering every record in order costs more in buffering than it saves. The worker count is `min(GOMAXPROCS, 8)`, chosen from measurements on real multi-core hardware. Every scan takes a context that is checked while records are being rejected, so cancelling stops it promptly. A backward search scans windows sized in bytes (about one segment per worker), so each window can use the parallel path.

## ADR-018 — Measure relative times from the log's own clock

`since:15m` counts back from the newest record of the log, read from the end of the file, not from the machine's clock. Most logs carry no time zone, so a wall-clock comparison would silently show everything (or nothing). Timestamps are compared by their wall reading; times given with an explicit zone are compared as instants. Records without a timestamp are kept, because hiding data the parser could not date is worse than showing it.

## ADR-019 — Show many sources with colour, not with a wide column

Each source (file, compose service, pod) gets a stable colour slot, shown as a bar in the row gutter, with its full name in the header for the cursor row. The source column, which only had room for a truncated name, is hidden by default and its width goes to the message. Colours come from the terminal's own palette, avoid red (errors), and repeat after ten; repeats can be told apart by shape markers, which are opt-in. Slots are handed out in registration order and never change during a session.

## ADR-020 — Strip terminal escapes at ingestion and again at display

Container logs are full of colour codes that hide timestamps and levels from the parsers. Escape sequences are removed from record text when it is ingested, and the display sanitizer removes them (and C1 controls, bidirectional overrides and separators) from anything else, filenames included. The raw bytes stay on disk and are read lazily for the detail view.

## ADR-021 — Recognise container tooling's line decoration for piped input only, conservatively

`docker compose logs`, `kubectl logs --prefix` and `docker logs -t` put a name or time in front of every line. For piped input logi samples the start of the stream and, only when nearly every line matches (with look-alikes such as bracketed timestamps, tables and ordinary ISO-timestamp logs excluded), strips the decoration, uses the service or pod as the record's source, and frames multi-line records per source, since stack traces from different services interleave. An unrecognised stream is left exactly as it is. The result does not depend on how the stream is cut into reads.

## ADR-022 — Give the interface the terminal as standard input when input is piped

The pipe is saved for the session and the terminal is made the process's standard input before the interface starts, so keyboard and mouse handling is exactly that of a normal launch rather than a special input path.

## ADR-023 — Match case-insensitively with Unicode simple folding, with an ASCII fast path

ASCII queries keep an allocation-free byte search; queries with non-ASCII text use Unicode simple case folding (the same equivalence as `(?i)` in regular expressions), compiled once per query. Folding is rune-to-rune, so `ß` does not match `SS`.

## ADR-024 — Commit to a small, documented compatibility surface

From 1.0.0 the command line, configuration keys, environment variables, filter language, exit codes and parser names are covered by semantic versioning; the interface's appearance, message wording, performance figures and everything under `internal/` are not. See `docs/STABILITY.md`.
