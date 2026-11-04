# Implementation Baseline — logi 1.0.0

This release is the synchronized implementation baseline. The documentation in this directory describes the source tree as it exists in 1.0.0.

## Retained design principles

- bounded live retention as a first-class invariant;
- byte-aware memory control;
- terminal-first interaction;
- structured detail overlays;
- terminal output sanitization;
- real NCache fixtures and regression coverage;
- dependency restraint outside the TUI and filesystem watcher.
- conservative retained-memory accounting for live records, including structured fields;
- short-lived ring read locks during live search and bounded filter catch-up batches to protect ingestion latency.

## Current implementation

- lazy, offset-based static-file access with a small parsed-block cache; cached static records retain compact semantic data while original payloads remain on disk;
- progressive static indexing by logical-record offsets without retaining file contents;
- incremental live tailing with bounded record and byte retention;
- directory following with newest-file and all-files modes;
- rotation, truncation, recreation, and file-identity handling;
- generic, JSON, NCache, and NCache event parsing through a shared profile registry;
- adaptive NCache layout detection and row realignment;
- bounded multiline framing;
- literal, RE2 regex, and structured-field search;
- field-aware filtering and live filter catch-up;
- match highlighting in the column containing the match;
- structured field/value detail rendering and pretty-printed JSON detail;
- severity-consistent detail styling with terminal-output sanitization;
- richer column management, command palette, file picker, and mouse input;
- rebindable action-based keyboard controls;
- compact live status-bar severity counts and buffer occupancy;
- integration and scale fixtures;
- `logigen` synthetic workload generation;
- raw-text prefiltering and ordered parallel scanning of static files (`MatchRanger`);
- time windows (`since:`/`until:`, `--since`/`--until`) anchored on the log's own clock;
- Unicode case-insensitive literal search and filter terms;
- several files in one view, with per-source colour, a gutter bar, header file name and an event column for mixed NCache logs;
- piped standard input with a fully functional keyboard, including container (`docker compose`, `kubectl`) decoration;
- Docker `json-file` and Kubernetes CRI log formats; terminal escape stripping at ingestion;
- a grouped, self-checking `--help` and conservative, case-insensitive command-line validation.

## Record representation and memory model

`LogRecord.Fields` uses a compact slice of structured field values rather than `map[string]any`. Log records normally contain only a small number of fields, so a linear lookup avoids map buckets and interface-value overhead while keeping the record representation simple. Field values are retained as strings; a small flag records values that originated as non-string JSON.

Static-store records detach their raw payload before being cached. Semantic strings are copied so they do not retain the original record buffer, while the source offset and length remain available for lazy raw reads. Live records may retain their raw text because it is already part of the bounded ring payload.

This representation is intentionally optimized for the common case: a few structured fields per record, bounded live storage, and no secondary field index.

## Configuration contract

The configuration precedence is:

```text
defaults < config file < LOGI_* environment < CLI flags
```

Configuration-file keys use snake_case. CLI options use GNU-style kebab-case. Multi-character CLI options require `--`; `-f` and `-h` are the available short forms.

## Parser terminology

A compiled-in **plugin** registers one or more **profiles**. A **profile** provides detection, parser construction, columns, and optional framing. A **parser** converts an already-framed record into the common `LogRecord` representation.

The NCache plugin is enabled by default and registers `ncache` and `ncache-events` profiles.


## Source positions

The engine exposes `SourcePosition` as a small, payload-free locator. Live stores resolve positions by ring record ID and reject stale/evicted IDs; static stores validate source identity and byte locator, then resolve through their lazy record cache. UI state such as bookmarks is intentionally separate from record data and position identity.

### Source positions in query results

Filtered rows are retained as compact `uint64` record IDs rather than full
`engine.SourcePosition` values. Source positions are materialized only when a
caller requests a stable locator, avoiding repeated source-string headers and
static byte offsets for every match. Search traversal captures the position from
the matching record itself and returns it with the result, avoiding a second
store lookup after a successful match. A source position carries a source
identity and storage locator: a record ID for live ring entries, or record ID
plus byte offset and length for static-file entries. The filter index retains no
record payloads.
Search completion events also include a source position. The TUI resolves that
position before moving the cursor, so a live match evicted between scanning and
UI delivery is reported as expired instead of silently selecting a different
record. The legacy ID remains in the event for compatibility with row-based
navigation.


## Query-state ownership

In 1.0.0, `Session` remains the coordinator for input lifecycle and worker scheduling. Filtered-row state is grouped in `filteredView`, and search generation, cancellation, and matcher ownership are grouped in `searchState`. Starting a search cancels its predecessor and advances a generation token; consumers can reject results from older generations. This is an incremental responsibility boundary, not a wholesale rewrite of `Session`.


## Filter-index ownership

In 1.0.0, `filteredView` encapsulates filter query replacement, generation tokens, scan cursors, batch commits, match-cap enforcement, eviction trimming, row mapping, and filter progress snapshots. `Session` retains responsibility for source lifecycle, scan scheduling, and record traversal; it no longer directly mutates the filtered-row ID index. Generation checks ensure that batches produced for a superseded filter cannot be committed. Live views retain the newest matching IDs in a circular buffer at the configured cap and continue catching up as the ring advances; static views stop scanning at the cap. Live catch-up uses smaller batches to limit read-lock duration. Static cache hits promote LRU entries without allocation, concurrent block misses are deduplicated when publishing to the cache, and range/cache reads reuse pooled buffers capped at the 2 MiB sequential-read target. The tailer clears its reusable parsed-record slice after storage and releases parser-detection samples once parser construction completes.


## Static cache memory bound

The static record cache is bounded by both a maximum block count and an estimated retained-byte budget (16 MiB by default). Blocks larger than the budget are returned for the current read but are not retained. This keeps random-access convenience from defeating the low-memory goal when records are unusually large. The byte count is conservative and is an estimate, not an exact measurement of Go heap ownership.

## Timer reuse

The live tailer and update emitter reuse coalescing timers rather than creating a new `time.After` timer for each event burst. The timer remains cancelable through the existing context select.
