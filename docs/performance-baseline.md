# Performance baseline

> The micro-benchmark figures below were measured on 0.4.0 and are kept as a regression baseline. Results for the 1.0 scan path (raw prefilter, parallel scanning, sanitizer, redraw) and a multi-core sample are in [`BENCHMARKS.md`](../BENCHMARKS.md).

This document records a repeatable baseline for the hottest dependency-light paths. These are measurements from one Linux amd64 environment using Go 1.23.2; they are not a promise for other machines and are not a comparison against a pre-optimization build.

## Design constraints

- Live record retention is bounded by both record capacity and an estimated byte budget.
- Static indexing retains compact offset/line checkpoints, not parsed records for the entire file.
- Static parsed-block caching is bounded by block count and estimated retained bytes (16 MiB by default); an oversized block is readable but is not cached.
- Filtering stores compact record IDs, uses bounded batches for live sources, and uses a circular buffer for capped live matches.
- Search and filter matching compile predicates once per query; case-insensitive literal matching (ASCII fast path, Unicode simple folding for non-ASCII queries) does not allocate per record.
- File-tail and UI-update coalescing reuse timers instead of allocating a new timer per event burst.
- Static range reads stream checkpoint blocks through the framer in chunks capped at 2 MiB, even when an individual checkpoint block is much larger; reusable scratch buffers above the pool threshold are discarded.
- The TUI caches its visible-column index list until column visibility or the detected layout changes, and calculates fixed row width once per rendered screen batch instead of once per row.

## Benchmark command

```sh
GOTOOLCHAIN=local go test -run '^$' -bench 'Benchmark(LiteralMatchCaseInsensitive|RingAppend|GenericParse|JSONParse|LineFramer)' -benchmem ./internal/engine ./internal/parser ./internal/framing ./internal/search
```

The module declares Go 1.25.0 (see `go.mod`); the figures in this file were measured earlier on Go 1.23.2 and are kept only as a historical baseline. The dependency-light benchmark command can run offline; packages using third-party modules still require their module checksums and downloaded sources.

## Observed results

| Benchmark | Time | Bytes/op | Allocs/op |
|---|---:|---:|---:|
| Ring append | 175.3 ns/op | 1 | 0 |
| Generic parse | 226.4 ns/op | 0 | 0 |
| JSON parse | 406.2 ns/op | 0 | 0 |
| Line framing | 47.8 µs/op (about 2.05 GB/s) | 8 | 0 |
| Case-insensitive literal match | 96.6 ns/op | 0 | 0 |

These numbers are useful as a local regression baseline, not as before/after proof. Re-run on the release Go version and representative Windows/Linux systems before making release performance claims.


## Static range benchmark

```sh
GOTOOLCHAIN=local go test -run '^$' -bench '^BenchmarkStaticRangeLargeCheckpointBlock$' -benchmem ./internal/input
```

This benchmark scans a roughly 3 MiB checkpoint block containing 96 records. A local one-iteration smoke run completed in about 3.2 ms and reported 6.1 MB allocated across record parsing and callbacks. This is a smoke measurement, not a statistically stable baseline or a before/after comparison. The main improvement is structural: the file-read scratch buffer is capped at 2 MiB instead of scaling with the entire checkpoint block.


## Release-readiness benchmark run (2026-10-11)

A fresh three-iteration smoke run on Linux amd64, Go 1.23.2, AMD EPYC 9V74 reported the following. These figures are environment-specific; compare only against future runs on a similar machine and Go toolchain. The static-range and filtered-view benchmarks were run with a temporary local `fsnotify` compile shim because the real module source/checksum was unavailable in this environment; those two benchmarks do not exercise filesystem notification behavior.

| Benchmark | Time | Bytes/op | Allocs/op |
|---|---:|---:|---:|
| Ring append | 46.2–48.8 ns/op | 0 | 0 |
| Generic parse | 148.5–152.6 ns/op | 0 | 0 |
| JSON parse | 168.2–169.6 ns/op | 0 | 0 |
| Line framing | 25.6–25.7 µs/op (about 3.81–3.83 GB/s) | 4 | 0 |
| Case-insensitive literal match | 45.0–46.5 ns/op | 0 | 0 |
| NCache columnar cache parse | 295.1–308.3 ns/op | 160 | 1 |
| NCache columnar event parse | 204.3–212.6 ns/op | 160 | 1 |
| NCache gap fallback parse | 419.4–436.7 ns/op | 400 | 5 |
| Static range, 96 × 32 KiB records | 3.28–3.51 ms/op | about 4.02–4.04 MB | 110 |
| Live filtered-view capped commit | 21.7–22.6 ns/op | 0 | 0 |

The core parse, framing, search, and ring benchmarks ran without third-party module substitutions. Results are smoke baselines, not before/after proof or release performance guarantees.
