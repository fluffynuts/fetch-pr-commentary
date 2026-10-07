package appcli

import (
	"runtime/debug"
	"strings"
)

// Version is the major.minor part of this build's version (e.g. "0.1"): the
// VERSION file at the root of the repository, bumped by hand, which the build
// scripts pass in with -ldflags "-X github.com/fluffynuts/fetch-pr-commentary/internal/appcli.Version=...".
// A plain `go build` leaves it as "dev".
var Version = "dev"

// Build is the CI build number a release was made from, appended to Version
// as its third part ("0.1.57"): set the same way as Version, and empty for
// any other build. VERSION is bumped by hand and every push to the release
// branch is released, so this is what tells those releases apart, and keeps
// each release a valid semantic version that sorts after the last.
var Build string

// BuildDate is when this binary was built, in UTC ("2026-09-30T11:22:05Z").
// Go doesn't record it — builds are reproducible on purpose — so the build
// scripts pass it in; a plain `go build` leaves it empty, and --version then
// leaves it out.
var BuildDate string

// Commit is the git commit this binary was built from, short form, with a
// "-dirty" suffix when the working tree had uncommitted changes — or empty
// when that isn't known (a build outside a git checkout, or with
// -buildvcs=false). `go build` records it by itself, so nothing needs
// passing in; it can still be overridden with
// -ldflags "-X github.com/fluffynuts/fetch-pr-commentary/internal/appcli.Commit=..." — which only works on
// a variable with no initialiser, hence init rather than "= vcsCommit()".
var Commit string

func init() {
	if Commit == "" {
		Commit = vcsCommit()
	}
}

// FullVersion is Version with the build number, when there is one.
func FullVersion() string {
	if Build == "" {
		return Version
	}
	return Version + "." + Build
}

// String is how the program describes itself:
// "fetch-pr-commentary 0.1.57 (a1b2c3d4e5f6, built 2026-09-30T11:22:05Z)", leaving out
// whatever this build doesn't know.
func String() string {
	var details []string
	if Commit != "" {
		details = append(details, Commit)
	}
	if BuildDate != "" {
		details = append(details, "built "+BuildDate)
	}
	if len(details) == 0 {
		return AppName + " " + FullVersion()
	}
	return AppName + " " + FullVersion() + " (" + strings.Join(details, ", ") + ")"
}

func vcsCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if revision != "" && modified {
		revision += "-dirty"
	}
	return revision
}
