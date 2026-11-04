#!/usr/bin/env bash
set -euo pipefail
VERSION="${1:?usage: scripts/release.sh <version>   e.g. scripts/release.sh 1.0.0}"
VERSION="${VERSION#v}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${ROOT}/dist"
rm -rf "$OUT"; mkdir -p "$OUT"
LDFLAGS="-s -w -X main.version=v${VERSION}"
build() {
  # separate statements: with `set -u`, a variable assigned in the same `local` is not yet set
  local os="$1" arch="$2" ext="$3"
  local name="logi_${VERSION}_${os}_${arch}"
  local tmp="${OUT}/${name}"
  mkdir -p "$tmp"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "${tmp}/logi${ext}" ./cmd/logi
  cp README.md LICENSE CHANGELOG.md "$tmp/"
  mkdir -p "$tmp/docs"
  cp docs/HOWTO.md docs/config.example.yaml docs/STABILITY.md "$tmp/docs/"
  if [[ "$os" == windows ]]; then (cd "$OUT" && zip -qr "${name}.zip" "$name"); else tar -C "$OUT" -czf "${OUT}/${name}.tar.gz" "$name"; fi
  rm -rf "$tmp"
}
build linux amd64 ''
build linux arm64 ''
build darwin amd64 ''
build darwin arm64 ''
build windows amd64 '.exe'
build windows arm64 '.exe'
(cd "$OUT" && sha256sum *.tar.gz *.zip > SHA256SUMS)
