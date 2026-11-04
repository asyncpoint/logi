# Performance baselines and concurrency checks

Phase 17 adds repeatable Go benchmarks and a bounded concurrent-reader/writer
stress test. It intentionally does not change production algorithms or add
runtime dependencies: first collect measurements, then optimize based on profiles.

## Run the suite

```sh
go test ./...
go test -race ./...
go vet ./...
go test ./internal/engine ./internal/parser ./internal/filter ./internal/search -run '^$' -bench . -benchmem
```

Benchmarks report allocations and bytes/op where applicable. Record the Go
version, operating system, CPU, commit, and command alongside any numbers; do
not compare results from different machines as if they were equivalent.

## CPU and memory profiles

```sh
go test ./internal/engine -run '^$' -bench 'BenchmarkRing' -benchmem -cpuprofile cpu.out -memprofile mem.out
go tool pprof cpu.out
go tool pprof mem.out
```

Profiles are local artifacts and should not be committed unless explicitly
needed for a reproducible investigation.

## Coverage

- `internal/engine`: bounded ring append at different capacities, point lookup,
  range iteration, parallel append, and concurrent append/read/eviction.
- `internal/parser`: generic and JSON record parsing.
- `internal/filter`: compile-once predicate matching for severity/text, fields,
  and regex filters.
- `internal/search`: literal and regex matching.

The concurrency test is a stress/regression test, not a proof of correctness
under every schedule. Keep `-race` enabled when running it. The ring's `Range`
callback executes while an `RLock` is held; callbacks must remain short and must
not call back into the same store. The eviction callback similarly runs under
the write lock and must be cheap and non-reentrant.

## Baseline policy

No absolute throughput target is encoded in tests. Use benchmark deltas and
profiles to identify regressions; avoid brittle CI thresholds until results
are collected on a controlled runner over multiple runs.

## Initial sample (Linux/amd64, Go 1.23.2)

A short local run (`-benchtime=100ms`) on an AMD EPYC 9V74 reported approximately:

| Operation | Sample time | Allocations/op |
|---|---:|---:|
| Ring append (new capacity benchmark) | 40–43 ns | 0 |
| Ring point lookup | 19 ns | 0 |
| Ring range of 100 records | 219 ns | 0 |
| Filter severity + text | 42 ns | 0 |
| Filter structured field | 21 ns | 0 |
| Filter regex | 160 ns | 0 |
| Generic parse | 138 ns | 0 |
| JSON parse | 227 ns | 0 |
| Literal search | 36 ns | 0 |
| Regex search | 807 ns | 0 |

These are smoke-test measurements, not release-grade performance claims: the
run was short, the machine is virtualized, and benchmark variants differ from
some pre-existing benchmarks. Re-run multiple times on the target developer
machine before using these values as a comparison baseline. TUI rendering and
end-to-end static indexing are not yet benchmarked by this phase.

## Static scan, sanitizer and fuzzing (0.4.1 work)

Run: `go test ./internal/input -run '^$' -bench Static -benchmem` and
`go test ./internal/engine -run '^$' -bench Sanitize -benchmem`.
`make fuzz` runs every fuzz target (`FUZZTIME=2m make fuzz` for longer).

Measured in a 1-CPU sandbox (Go 1.22, warm page cache, 280 MB / 246 MB generated
logs, so compare *ratios*, not absolute numbers):

| Operation | Before | After |
|---|---:|---:|
| Static literal search, plain-text log | 230 MB/s | 532–588 MB/s |
| Static literal search, JSON log | 205–213 MB/s | 766–1034 MB/s |
| Case-insensitive substring miss (`ContainsFold`) | 494 MB/s | ~985 MB/s |
| Redraw of 50 scattered filter matches (warm) | 5.5 ms | 5.5 µs |
| Tailer allocation on a quiet log | ~112 KiB/record | < 16 KiB/record |
| `SanitizeText` on a clean ASCII line | 314 ns | 79 ns |
| `LogRecord` size / full GC of a 1M-record ring | 152 B / 51 ms | 144 B / 42 ms |

### Multi-core sample (Windows/amd64, Go 1.23.2, Intel i5-8250U 4C/8T)

`go test ./internal/input -run '^$' -bench Static -benchmem` on a ~25 MB file:

| Benchmark | Throughput | Allocations/op |
|---|---:|---:|
| `StaticRangeParseAll` (parse and deliver every record) | 242 MB/s | 250,086 |
| `StaticSearchNoPrefilter` | 167 MB/s | 250,083 |
| `StaticSearchRawPrefilter` (1 worker) | 445 MB/s (2.7x) | 64 |
| `StaticSearchParallel4` (4 workers) | 1133 MB/s (2.55x over 1 worker) | 111 |
| `StaticScatteredRedraw` (50 scattered rows) | 3.8 µs | 0 |

Parallel `Range` preserves file order and bounded memory. It is used only when
the scan is sparse (a raw `keep` prefilter and/or a parsed-record `accept`
predicate is supplied): delivering every record in order costs more in buffering
than parallelism recovers (measured 2.4-3.5x slower on one CPU), so plain
`Range` stays sequential. `accept` and `keep` run on the workers and must be safe
for concurrent use (compiled filters and matchers are immutable). The default
worker count is `min(GOMAXPROCS, 8)`; `StaticOptions.ScanWorkers` overrides it.
To tune it for a machine, compare `BenchmarkStaticSearchScaling`,
`BenchmarkStaticParseSparseScaling` and `BenchmarkStaticParseDenseScaling`
(workers = 1, 2, 4, 8). The dense variant accepts every record, so it measures
the cost of ordered delivery; it should be at least as fast as `workers=1`.

Worker-count sweep on the same machine (`-bench Scaling`):

| Workers | Search, raw prefilter | Parse-heavy filter (`severity:fatal`) |
|---:|---:|---:|
| 1 | 406 MB/s | 238 MB/s |
| 2 | 783 MB/s (1.9x) | 414 MB/s (1.7x) |
| 4 | 1355 MB/s (3.3x) | 614 MB/s (2.6x) |
| 8 | 1275 MB/s (3.1x) | 703 MB/s (3.0x) |

Light, memory-bound scans peak at one worker per physical core; CPU-bound
parsing keeps gaining from hyper-threads. Because the slow (parse-bound) case
gains more at 8 than the fast case loses, the default stays `min(GOMAXPROCS, 8)`.
On machines with many more than 8 threads, re-run the sweep before raising the cap.

A scan allocates its read buffers (up to workers x 2 MiB) per call rather than
keeping them while idle; this is a fixed cost per scan, not per byte.

Fuzz targets: `FuzzJSONUnescape`, `FuzzParsers`, `FuzzSanitizeText`,
`FuzzFramerSplitInvariance` (framing independent of read size, no overlaps,
limits respected), `FuzzContainsFold` (vs. a byte-wise reference),
`FuzzFilterPrefilterIsNecessary` and `FuzzMatcherRawKeep` (the raw prefilter must
never reject a record the full predicate accepts), `FuzzNCacheParse`.
