# fetch-pr-commentary: build, test and tidy the CLI.
#
# Nothing in here lists source files or packages, so it keeps working as the
# project grows: sources are found with `find`, and tests, vet and the
# release zips cover whatever `./...` covers. Only PKG, the folder holding
# the program's main package, needs to change if main moves.
#
# make.sh and make.ps1 do the same jobs on machines without make.

GO     ?= go
BINARY ?= fetch-pr-commentary
PKG    ?= ./src

MODULE  := github.com/fluffynuts/fetch-pr-commentary
VERSION := $(shell tr -d '[:space:]' < VERSION)
APPCLI  := $(MODULE)/internal/appcli

SOURCES := $(shell find . -name '*.go' -not -path './.git/*')

.DEFAULT_GOAL := build

.PHONY: build
build: $(BINARY)

# The version comes from the VERSION file (major.minor, bumped by hand) and
# the build date goes into --version; Go doesn't record it itself.
$(BINARY): $(SOURCES) go.mod $(wildcard go.sum) VERSION
	$(GO) build -ldflags "-X $(APPCLI).Version=$(VERSION) -X $(APPCLI).BuildDate=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o $@ $(PKG)

.PHONY: test
test:
	$(GO) test ./...

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: check
check: vet test

# A release zip for GOOS/GOARCH (default: this machine) in dist/ — see
# make.sh, which does the packaging for both.
.PHONY: dist
dist:
	./make.sh dist

.PHONY: clean
clean:
	rm -f $(BINARY)
	rm -rf dist
