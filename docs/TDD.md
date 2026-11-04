# Technical Design Document

This document is the technical baseline for **logi 1.0.0**. It describes the implementation that ships in this source tree; it is not a historical plan for an earlier release.

## Goals

logi is meant to remain terminal-first, fast to start, conservative with CPU/RAM/disk, useful for continuously changing logs, and pleasant for developers who already live in terminals and IDEs.

The core invariants are:

- static files are browsed lazily rather than loaded wholesale into RAM;
- live retention is bounded by record count and approximate bytes;
- records are framed incrementally and capped by size and multiline-line limits;
- the engine does not depend on the TUI;
- log content is treated as untrusted terminal data;
- there is no persistent on-disk index by default.

### Resource invariants

logi treats resource usage as part of the architecture, not as a later optimization concern:

- live memory is bounded by the configured record capacity and a conservative retained-memory byte estimate;
- the byte estimate includes structured-field/container overhead, not only the raw log payload;
- ingestion work is proportional to newly arrived bytes/records rather than the full source history;
- rendering is proportional to the visible terminal window rather than the retained record count;
- live searches do not hold the ring read lock across an entire scan, so a long search cannot pause ingestion for its full duration;
- live filter catch-up uses bounded batches so filtering yields regularly to new ingestion;
- searches and filters do not create persistent indexes or disk caches.
- search results carry payload-free source positions captured during traversal, avoiding a second store lookup after a match; the UI validates the position before navigation.

The practical performance targets remain a small idle footprint, negligible work when a followed source is unchanged, and a typical live-monitoring heap comfortably below 40 MiB for normal configurations. The scale suite uses a looser 64 MiB regression ceiling to account for Go runtime and test-process overhead.

## Architecture

```text
CLI / config
     ↓
   Session  ←────────────── Parser Registry / built-in plugins
     ↓
Input source
 ├─ StaticStore ── lazy offsets + parsed-block cache
 └─ Tailer(s) ─── framing → parsing → bounded RingStore
                         ↓
                    LogRecord / Stats
                         ↓
              Search / Filter / View mapping
                         ↓
                    Bubble Tea TUI
```

`Session` is the application coordinator for source lifecycle, record access, status, and background workers. The mutable filtered-row index is grouped in `filteredView`, while `searchState` owns query cancellation, generation tokens, and the active matcher. This keeps query lifecycle state separate from source lifecycle without introducing a second top-level controller. The engine and input packages remain independent of the TUI.

`SourcePosition` is the payload-free identity boundary for navigation and query results. Live positions contain a source identity and ring record ID, so they become stale after eviction; static positions contain a source identity, logical record ID, and byte offset/length so cached records can be re-resolved lazily. Bookmark metadata belongs in a separate session/UI collection keyed by `SourcePosition`, never in `LogRecord`.

### Static input

A static file is opened once and indexed progressively. The index stores logical-record checkpoints and offsets, not the file contents. Building the index therefore reads the source file, but it does not parse every record into retained memory. Navigation loads parsed blocks on demand and keeps only a small cache. Cached static records retain compact semantic data only; their original payload remains on disk and is fetched lazily through the store. A sequential Range scan may temporarily expose the raw record to its callback, but that payload is not retained by the cache.

Searches and filters may scan a large portion of the file; those scans are performed progressively rather than by loading the whole file into RAM.

### Live input

A live file is read incrementally by a tailer. Bytes are framed into logical records, parsed, and inserted into a bounded ring. Directory mode creates one tailer per followed file and handles truncation, recreation, and rotation through file identity and watcher state.

## Configuration and precedence

Settings are resolved in this order, from lowest to highest priority:

```text
defaults
   ↓
config file
   ↓
LOGI_* environment variables
   ↓
CLI flags
```

The configuration file is a deliberately small YAML subset and is read-only. The canonical configuration names use snake_case, for example `limits.max_record_size`. CLI names use GNU-style kebab-case, for example `--max-record-size`.

Multi-character CLI options require `--`. The supported short options are `-f` for `--follow` and `-h` for `--help`. `--live` is accepted as a compatibility alias for `--follow`. Help output must include registered flag descriptions.

## Parser model

Three concepts are used consistently:

- **Plugin** — a compiled-in extension that can register one or more parser profiles.
- **Profile** — the description of one log format, including detection, parser construction, columns, and optional framing.
- **Parser** — converts one framed logical record into a `LogRecord`.

The core registry always provides `generic` and `json`. The NCache plugin is compiled in and enabled by default; it registers the `ncache` and `ncache-events` profiles. Plugins are not dynamically loaded at runtime.

`auto` selects the highest-confidence registered specialized profile and falls back to `generic`.

## Record representation and memory model

`LogRecord.Fields` uses a compact slice of structured field values rather than `map[string]any`. Log records normally contain only a small number of fields, so a linear lookup avoids map buckets and interface-value overhead while keeping the record representation simple. Field values are retained as strings; a small flag records values that originated as non-string JSON.

Static-store records detach their raw payload before being cached. Semantic strings are copied so they do not retain the original record buffer, while the source offset and length remain available for lazy raw reads. Live records may retain their raw text because it is already part of the bounded ring payload.

This representation is intentionally optimized for the common case: a few structured fields per record, bounded live storage, and no secondary field index.

## Framing and limits

Framing converts the byte stream into logical records before parsing. Generic framing can detect timestamp-started multiline records; JSON uses one object per physical line; a profile can provide its own framer.

The configured limits are:

- `max_record_size` — maximum logical record bytes;
- `max_lines` — maximum physical lines in a multiline record;
- `multiline: off` — strict one-record-per-line framing.

When a limit is reached, the record is marked truncated while source offsets remain usable.

## Search and filtering

Search and filtering are separate.

Search supports:

- literal text;
- RE2 regular expressions;
- structured field matching.

Filtering compiles an expression into a predicate and maintains a filtered view without changing the underlying store. Live filtering also catches up with records that arrive after the filter is installed.

## TUI

The interface uses Bubble Tea v2 and Lip Gloss v2. The interaction model remains terminal-first: short keys, overlays instead of full-screen mode changes, and `Enter` for record inspection.

The detail overlay uses the parser's known columns for non-JSON records and pretty-prints JSON records while preserving their native structure. Severity styling in the detail view uses the same semantic severity mapping as the normal list view; rendered content is sanitized so ANSI/SGR sequences from log data do not leak into the UI.

Normal-view shortcut hints live in the status bar rather than consuming table rows. The status bar also reports compact retained severity counts (`C`, `E`, `W`) and live buffer occupancy where applicable.

Columns can be hidden/restored and navigated without leaving the keyboard. The command palette and file picker are exposed as actions rather than being coupled directly to individual key handlers.

## NCache

NCache is a built-in plugin because it is a first-class supported workload. The plugin supports separate cache-log and event-log profiles, detects real headers/layouts, validates rows against inferred boundaries, and realigns imperfectly padded exports against data rows.

The plugin is controlled as one plugin (`ncache`) even though it registers two parser profiles (`ncache` and `ncache-events`).

## Scanning, time windows and sources

Searching and filtering a static file go through `MatchRanger`: a raw-text prefilter rejects records before they are parsed, and sparse scans run on parallel workers with in-order delivery (ADR-016, ADR-017). `since:`/`until:` terms are measured from the newest record of the log (ADR-018). Records from several files, or from the services of a piped compose or `kubectl` stream, carry a source with a stable colour slot (ADR-019, ADR-021).

## Piped input

Standard input is read by the live tailer in stream mode through the same framing, parsing and bounded ring as a file; when the stream ends the view stays open. The terminal is made the process's standard input so the interface behaves as in a normal launch (ADR-022).

## Safety

Rendered log content is sanitized before it is sent to the terminal, and terminal escape sequences are stripped from log text as it is ingested (ADR-020). Log input is treated as untrusted data.

## Testing and release

The repository contains unit tests for configuration, framing, parsing, filtering, search, input, engine behavior, plugins, and TUI features, plus integration and scale fixtures and the `logigen` workload generator.

Parsers, the framer, the filter and search matchers, the sanitizer and the stream detector have fuzz targets (`make fuzz`; a short run is part of CI), including property tests that the raw prefilter never rejects a match and that framing does not depend on how input is split into reads. `BENCHMARKS.md` records the scan, redraw and sanitizer benchmarks.

The project targets the Go version declared in `go.mod` (1.25.0) and produces Linux, Windows, and macOS binaries. The Makefile and GitHub workflows use the same toolchain requirement as `go.mod`. A release is a `vX.Y.Z` tag: the release workflow checks that `go.mod` is tidy, runs vet and the race-detector tests, builds the archives for six platform/architecture pairs with `scripts/release.sh X.Y.Z`, and publishes them with `SHA256SUMS`. Compatibility rules are in `docs/STABILITY.md`.

#### Source-position query invariants

- Filter indexes retain compact `uint64` record IDs, not parsed records, raw log
  text, or repeated source-position metadata. Source positions are materialized
  when a caller requests a stable locator.
- Static positions include source identity, record ID, byte offset, and length;
  resolution rejects mismatched source identities or changed locators.
- Live positions resolve only while the corresponding record remains in the
  bounded ring, and multi-file live sessions validate the record's own source
  identity.
- Search results carry a source position and the UI verifies it before
  selecting the match, handling ring eviction between search and delivery.


### Filtered-view ownership

The `filteredView` type owns the mutable filter index and exposes operations for starting a query, retrieving the scan cursor, committing a generation-checked batch, trimming evicted live positions, and reading row/progress state. `Session` owns the worker loop and record-source traversal. This boundary keeps the index mutation rules in one place and allows tests to cover stale batches, result caps, and eviction trimming without starting an input source.
