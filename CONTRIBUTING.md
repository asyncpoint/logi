# Contributing

Contributions to `logi` are welcome. The project aims to stay lightweight, predictable, cross-platform and easy to understand.

## Development

Development needs the Go version declared in `go.mod` (1.25.0 or newer) and a terminal for interactive TUI testing.

Run the standard checks with:

```bash
go test ./...
go test -race ./...
go vet ./...
make lint        # staticcheck and govulncheck, as run by the analysis job in CI
```

Build locally with:

```bash
make build
```

Cross-compile the supported release targets with:

```bash
make dist
```

`make fuzz` runs the fuzz targets and `make bench` the benchmarks.

## Design principles

`logi` stays lightweight. Bounded memory, streaming processing, simple interfaces and measured optimization are preferred over speculative complexity.

Parser-specific behavior lives in plugins. The engine stays format-independent, and the TUI stays focused on consuming normalized engine data rather than parsing logs itself.

Changes to performance-sensitive code come with a benchmark of the affected path and an explanation of the reason for the change, rather than optimization by assumption.

## Documentation

`README.md` and `docs/HOWTO.md` are checked by tests: every command-line example must parse, every option must be documented, every default key binding must appear in the README key table, and the documents must not be written in the first person. A change that adds an option, key or configuration key therefore also updates those documents and `docs/config.example.yaml`. `CHANGELOG.md` records user-visible changes.

## Releasing

Binaries are built by GitHub Actions; nothing is built by hand. Every push builds and smoke-tests the six archives (Linux, macOS and Windows, each for amd64 and arm64) and keeps them as workflow artifacts. A release is made by tag:

1. Make sure `main` is green.
2. In `CHANGELOG.md`, rename `## Unreleased` to `## X.Y.Z — <date>`. Choose the version by semantic versioning: a fix is a patch, a new feature is a minor release, and only a major release may break what `docs/STABILITY.md` lists.
3. Commit, then `git tag vX.Y.Z` and `git push origin vX.Y.Z`.

The release workflow refuses a tag whose version has no changelog section, verifies the module, runs the tests, builds the archives, and publishes them with `SHA256SUMS` and the changelog entry. A tag with a suffix such as `v1.2.0-rc.1` is published as a pre-release. To try the build without publishing, run the *Release* workflow by hand from the Actions tab. Locally, `bash scripts/release.sh X.Y.Z` produces the same archives in `dist/`.

## Pull requests

A change that alters behavior comes with tests, and the complete verification suite (`go vet ./...` and `go test -race ./...`) passes before the change is considered ready for review.

For TUI changes, the description includes the affected keyboard behavior or visual interaction. For parser changes, representative fixtures are included whenever possible.
