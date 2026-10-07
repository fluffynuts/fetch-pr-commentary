#!/usr/bin/env bash
# fetch-pr-commentary: build, test and tidy the CLI, for machines without make.
# Mirrors the Makefile: ./make.sh [build|test|vet|check|clean|dist]... (default: build)
#
# GO, BINARY and PKG can be overridden from the environment, as with make.
#
# dist packages a release zip in dist/: the binary plus whatever BUNDLE
# (below) lists, under one top-level folder. It builds for GOOS/GOARCH when
# those are set (cross-compiling, with cgo off), else for this machine.
# BUILD, when set, is the CI build number: it becomes the version's third
# part, in the name and in the binary (fetch-pr-commentary --version). DIST_LABEL, when
# set, goes into the name too: fetch-pr-commentary-<version>[.<build>][-<label>]-<os>-<arch>.
# The zip is made with zip(1) so the binary keeps its executable bit.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

GO="${GO:-go}"
BINARY="${BINARY:-fetch-pr-commentary}"
PKG="${PKG:-./src}"
APPCLI="github.com/fluffynuts/fetch-pr-commentary/internal/appcli"
VERSION="$(tr -d '[:space:]' <VERSION)"

# When this build ran, for --version: Go doesn't record it itself.
build_date() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# build only when a source is newer than the binary, as make would.
target_build() {
  if [[ -f "$BINARY" ]] && [[ -z "$(find . \( -name '*.go' -o -name go.mod -o -name go.sum -o -name VERSION \) \
      -not -path './.git/*' -newer "$BINARY" -print -quit)" ]]; then
    echo "'$BINARY' is up to date."
    return
  fi
  echo "$GO build -o $BINARY $PKG"
  "$GO" build -ldflags "-X $APPCLI.Version=$VERSION -X $APPCLI.BuildDate=$(build_date)" -o "$BINARY" "$PKG"
}

target_test() {
  echo "$GO test ./..."
  "$GO" test ./...
}

target_vet() {
  echo "$GO vet ./..."
  "$GO" vet ./...
}

target_check() {
  target_vet
  target_test
}

# What goes into the release zip beside the binary: files and folders,
# relative to the root of the repository. Most programs need nothing but a
# readme. To ship more (config, templates, docs...), add them here, e.g.
#
#   BUNDLE=(README.md LICENSE config defaults)
#
# and, if the program should find them next to itself at run time, make
# sure --install copies them too (it only copies the binary). Add the same
# names to BUNDLE in make.ps1. Nothing in the GitHub workflow needs to
# change: it releases whatever dist makes.
BUNDLE=(README.md)

target_dist() {
  local goos goarch os_name version name exe stage item
  command -v zip >/dev/null 2>&1 || { echo "make.sh: dist needs zip(1), which isn't installed" >&2; exit 1; }
  goos="${GOOS:-$("$GO" env GOOS)}"
  goarch="${GOARCH:-$("$GO" env GOARCH)}"
  os_name="$goos"
  [[ "$goos" == darwin ]] && os_name=macos
  version="$VERSION${BUILD:+.$BUILD}"
  name="fetch-pr-commentary-$version${DIST_LABEL:+-$DIST_LABEL}-$os_name-$goarch"
  exe=fetch-pr-commentary
  [[ "$goos" == windows ]] && exe=fetch-pr-commentary.exe
  stage="dist/$name"

  rm -rf "$stage" "$stage.zip"
  mkdir -p "$stage"
  echo "GOOS=$goos GOARCH=$goarch $GO build -o $stage/$exe $PKG" >&2
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$GO" build -trimpath \
    -ldflags "-X $APPCLI.Version=$VERSION -X $APPCLI.Build=${BUILD:-} -X $APPCLI.BuildDate=$(build_date)" \
    -o "$stage/$exe" "$PKG"
  for item in "${BUNDLE[@]}"; do
    if [[ -e "$item" ]]; then
      cp -R "$item" "$stage/"
    else
      echo "make.sh: warning: BUNDLE lists '$item', which doesn't exist" >&2
    fi
  done
  (cd dist && zip -qrX "$name.zip" "$name")
  rm -rf "$stage"
  echo "dist/$name.zip"
}

target_clean() {
  echo "rm -f $BINARY"
  rm -f "$BINARY"
  echo "rm -rf dist"
  rm -rf dist
}

[[ $# -eq 0 ]] && set -- build

for target in "$@"; do
  case "$target" in
    build | test | vet | check | clean | dist) "target_$target" ;;
    *)
      echo "make.sh: no such target '$target' (build, test, vet, check, clean, dist)" >&2
      exit 2
      ;;
  esac
done
