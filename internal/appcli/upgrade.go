package appcli

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Nothing here needs a GitHub login, or GitHub's API (which limits anonymous
// callers to 60 requests an hour): the latest release's tag comes from where
// <releases>/latest redirects to, and every file is then downloaded from
// that tag, so a release published mid-upgrade can't mix versions.

// sumsFile is the release asset holding every zip's SHA-256.
const sumsFile = "SHA256SUMS"

var client = &http.Client{Timeout: 10 * time.Minute}

// Upgrade downloads the latest release's zip for this machine, checks it
// against the release's SHA256SUMS, unpacks it in a temporary folder and runs
// its --install, which puts the new binary where the old one was installed.
// It does nothing when the latest release isn't newer than this build,
// unless force is set.
func Upgrade(out io.Writer, force bool) error {
	tag, err := latestTag(Releases)
	if err != nil {
		return err
	}
	if !force && !Newer(strings.TrimPrefix(tag, "v"), FullVersion()) {
		fmt.Fprintf(out, "%s %s is up to date (the latest release is %s)\n", AppName, FullVersion(), tag)
		return nil
	}

	downloads, err := os.MkdirTemp("", AppName+"-download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(downloads)
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(out, "downloading %s from release %s\n", asset, tag)
	zipPath, err := download(Releases, tag, asset, downloads)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "checked %s against %s\n", asset, sumsFile)

	unpacked, err := os.MkdirTemp("", AppName+"-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(unpacked)
	folder, err := unzip(zipPath, unpacked)
	if err != nil {
		return err
	}
	exe := filepath.Join(folder, AppName)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("the release zip doesn't hold %s", filepath.Base(exe))
	}

	fmt.Fprintf(out, "running --install from the new release\n")
	cmd := exec.Command(exe, "--install")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the new release's --install didn't finish cleanly (see above): %w", err)
	}
	return nil
}

// AssetName is the release zip for an OS and architecture, as named in every
// release: fetch-pr-commentary-linux-amd64.zip, fetch-pr-commentary-macos-arm64.zip, ...
func AssetName(goos, goarch string) string {
	if goos == "darwin" {
		goos = "macos"
	}
	return AppName + "-" + goos + "-" + goarch + ".zip"
}

// latestTag returns the latest release's tag ("v0.1.57"), read from where
// the releases/latest page redirects to. GitHub's latest release is never one
// marked as a prerelease.
func latestTag(releases string) (string, error) {
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Get(releases + "/latest")
	if err != nil {
		return "", fmt.Errorf("asking GitHub for the latest release: %w", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode >= 400 || !strings.Contains(loc, "/releases/tag/") {
		return "", fmt.Errorf("asking GitHub for the latest release: got %s, not a redirect to a release (has one been published?)", resp.Status)
	}
	tag := path.Base(loc)
	if tag == "" || tag == "tag" {
		return "", fmt.Errorf("asking GitHub for the latest release: can't read a tag from %s", loc)
	}
	return tag, nil
}

// Newer reports whether release version latest ("0.1.57") is newer than
// running, which lacks its third part when it wasn't built by CI (a local
// build, which any release of the same version counts as newer than), and
// isn't a version at all for a plain `go build` ("dev", which everything
// is newer than).
func Newer(latest, running string) bool {
	l, r := versionParts(latest), versionParts(running)
	for i := 0; i < max(len(l), len(r)); i++ {
		lv, rv := partAt(l, i), partAt(r, i)
		if lv != rv {
			return lv > rv
		}
	}
	return false
}

func versionParts(v string) []int {
	var out []int
	for _, p := range strings.Split(strings.TrimPrefix(v, "v"), ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = -1
		}
		out = append(out, n)
	}
	return out
}

// partAt is a version's i'th part; a missing one sorts below any real one.
func partAt(p []int, i int) int {
	if i < len(p) {
		return p[i]
	}
	return -1
}

// download fetches tag's asset into dir, and checks it against the tag's
// SHA256SUMS. It returns the downloaded file's path.
func download(releases, tag, asset, dir string) (string, error) {
	sums, err := fetchSums(releases, tag)
	if err != nil {
		return "", err
	}
	want, ok := sums[asset]
	if !ok {
		return "", fmt.Errorf("release %s has no %s: is this platform built?", tag, asset)
	}
	dest := filepath.Join(dir, asset)
	if err := fetch(releases+"/download/"+tag+"/"+asset, dest); err != nil {
		return "", err
	}
	got, err := sha256File(dest)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf("%s doesn't match its checksum in %s (got %s, want %s): not using it", asset, sumsFile, got, want)
	}
	return dest, nil
}

func fetchSums(releases, tag string) (map[string]string, error) {
	resp, err := client.Get(releases + "/download/" + tag + "/" + sumsFile)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", sumsFile, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s for %s: %s", sumsFile, tag, resp.Status)
	}
	sums := map[string]string{}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		// sha256sum's format: "<hex>  <name>", or "<hex> *<name>" in binary mode.
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 {
			sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
		}
	}
	return sums, scanner.Err()
}

// fetch downloads url into the file dest.
func fetch(url, dest string) error {
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	return f.Close()
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// unzip unpacks a release zip into dir, keeping file modes (the binary's
// executable bit), and returns the one top-level folder it holds.
func unzip(zipPath, dir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	top := ""
	for _, f := range r.File {
		name := filepath.FromSlash(f.Name)
		// Every entry must land inside dir: a "../" in a name ("zip slip")
		// is refused, not followed.
		target := filepath.Join(dir, name)
		if rel, err := filepath.Rel(dir, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%s: entry %q would land outside %s", zipPath, f.Name, dir)
		}
		if first := strings.SplitN(filepath.ToSlash(name), "/", 2)[0]; top == "" {
			top = first
		} else if first != top {
			return "", fmt.Errorf("%s holds more than one top-level folder (%s, %s)", zipPath, top, first)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err := extract(f, target); err != nil {
			return "", err
		}
	}
	if top == "" {
		return "", errors.New(zipPath + " is empty")
	}
	return filepath.Join(dir, top), nil
}

func extract(f *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := f.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, mode) // past the umask, so 0755 stays 0755
}
