# logi 1.0.0

logi 1.0.0 is the first stable release. The command line, the configuration file, the filter language and the exit codes are now covered by semantic versioning (see [docs/STABILITY.md](docs/STABILITY.md)): anything that works in 1.0 keeps working in every 1.x release.

## Highlights

**Faster.** Searching and filtering a large static file is 2.3–5× faster on one core, because records that cannot match are rejected on their raw text before they are parsed, and about 3× faster on a 4-core machine because scanning runs in parallel with results still delivered in file order. Filtered views redraw instantly, a quiet live tail no longer allocates a large buffer per update, and records are smaller.

**Many files, one view.** `logi -f a.log b.log`, wildcards on Windows, and `--dir-mode all` give a merged view in which every file has its own colour: a bar in the row gutter, the full file name in the header, and an `event` column for NCache logs. The source column is hidden by default so the message gets the room.

**Docker and Kubernetes.** `docker compose logs -f | logi`, `kubectl logs -f … | logi` and `docker logs -f … 2>&1 | logi` work: services and pods become coloured sources, stack traces stay with their service, colour codes are removed, and Docker `json-file` and Kubernetes CRI log files are understood. Piping works with a fully functional keyboard.

**Time windows and history.** `--since 15m`, `--until 14:30` (also `since:` / `until:` in the filter bar) measure from the newest record of the log itself, so they work on old files and logs without a time zone. `-n 500` starts a live view from the last 500 lines.

**A friendlier command line.** Grouped `--help` with readable defaults, environment variables and recipes; `-V`; choices ignore case and suggest the nearest value; errors say what is wrong.

**Safer and more correct.** Terminal control sequences in log content (including C1 controls and bidirectional overrides) can no longer reach your terminal; JSON strings with `\/` and surrogate pairs decode correctly; case-insensitive search understands Unicode; year-less timestamps take their year from the file; and several races and cancellation problems found by fuzzing and review are fixed.

## Upgrading from 0.4.0

- In multi-file views the **source column is hidden by default**; use `--show-columns source` (or `X`) to bring it back. The file's colour bar and the header name replace it.
- Choices such as `--dir-mode` and `--color` now ignore case; nothing that worked before stops working.
- `--source-gutter` takes `auto|on|off`; a bare `--source-gutter` still means on.

## Verification

`go vet`, `go test -race ./...` and the fuzz targets (`make fuzz`) run in CI on Linux, Windows and macOS; the release workflow repeats them before building the archives and `SHA256SUMS`. See `BENCHMARKS.md` for the measurements behind the performance claims.
