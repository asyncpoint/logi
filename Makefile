# Requires the Go version declared in go.mod (1.25.0+). `make deps` needs network access once.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST    := dist

.PHONY: all deps build test race short bench fuzz fmt vet dist clean

all: build

deps:            ## fetch modules and write go.sum
	go mod tidy

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/logi ./cmd/logi
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/logigen ./cmd/logigen

test:
	go test ./...

short:           ## skip the multi-second scale tests
	go test -short ./...

race:
	go test -race ./...

# Short fuzz pass over every parser of untrusted input (FUZZTIME=2m for longer).
FUZZTIME ?= 20s
fuzz:
	go test ./internal/parser -run '^$$' -fuzz '^FuzzJSONUnescape$$' -fuzztime $(FUZZTIME)
	go test ./internal/parser -run '^$$' -fuzz '^FuzzParsers$$' -fuzztime $(FUZZTIME)
	go test ./internal/engine -run '^$$' -fuzz '^FuzzSanitizeText$$' -fuzztime $(FUZZTIME)
	go test ./internal/engine -run '^$$' -fuzz '^FuzzSanitizeKeepTabs$$' -fuzztime $(FUZZTIME)
	go test ./internal/engine -run '^$$' -fuzz '^FuzzStripANSI$$' -fuzztime $(FUZZTIME)
	go test ./internal/input -run '^$$' -fuzz '^FuzzDetectAndStrip$$' -fuzztime $(FUZZTIME)
	go test ./internal/framing -run '^$$' -fuzz '^FuzzFramerSplitInvariance$$' -fuzztime $(FUZZTIME)
	go test ./internal/framing -run '^$$' -fuzz '^FuzzLooksLikeTimestampNeverPanics$$' -fuzztime $(FUZZTIME)
	go test ./internal/filter -run '^$$' -fuzz '^FuzzContainsFold$$' -fuzztime $(FUZZTIME)
	go test ./internal/filter -run '^$$' -fuzz '^FuzzFilterPrefilterIsNecessary$$' -fuzztime $(FUZZTIME)
	go test ./internal/filter -run '^$$' -fuzz '^FuzzParseTimeSpec$$' -fuzztime $(FUZZTIME)
	go test ./internal/search -run '^$$' -fuzz '^FuzzMatcherRawKeep$$' -fuzztime $(FUZZTIME)
	go test ./internal/plugins/ncache -run '^$$' -fuzz '^FuzzNCacheParse$$' -fuzztime $(FUZZTIME)

bench:
	go test -run xxx -bench . -benchmem ./internal/... 

fmt:
	gofmt -l -w .

vet:
	go vet ./...
	GOOS=windows go vet ./...

dist:            ## cross-compile release binaries
	@mkdir -p $(DIST)
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/logi-linux-amd64       ./cmd/logi
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/logi-linux-arm64       ./cmd/logi
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/logi-windows-amd64.exe ./cmd/logi
	GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/logi-windows-arm64.exe ./cmd/logi
	GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/logi-darwin-arm64      ./cmd/logi
	GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/logi-darwin-amd64      ./cmd/logi

clean:
	rm -rf $(DIST)
