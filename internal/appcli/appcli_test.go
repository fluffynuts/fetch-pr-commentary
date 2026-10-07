package appcli

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHandleIgnoresTheProgramsOwnArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"run"}, {"run", "--install"}, {"--other"}} {
		if handled, _ := handle(args, &bytes.Buffer{}, &bytes.Buffer{}); handled {
			t.Errorf("%v: handled, but isn't one of ours", args)
		}
	}
}

func TestHandleHelpListsEveryOption(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		var out bytes.Buffer
		handled, code := handle([]string{flag}, &out, &bytes.Buffer{})
		if !handled || code != 0 {
			t.Fatalf("%s: handled=%v code=%d", flag, handled, code)
		}
		for _, want := range []string{"--help", "--version", "--install", "--upgrade", "Examples:", AppName + " --upgrade"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s output lacks %q:\n%s", flag, want, out.String())
			}
		}
	}
}

func TestHandleVersion(t *testing.T) {
	for _, flag := range []string{"-v", "--version"} {
		var out bytes.Buffer
		handled, code := handle([]string{flag}, &out, &bytes.Buffer{})
		if !handled || code != 0 {
			t.Fatalf("%s: handled=%v code=%d", flag, handled, code)
		}
		if got := strings.TrimSpace(out.String()); got != String() {
			t.Errorf("%s printed %q, want %q", flag, got, String())
		}
	}
}

func TestHandleRefusesStrayArguments(t *testing.T) {
	for _, args := range [][]string{{"--install", "now"}, {"--upgrade", "--later"}} {
		var stderr bytes.Buffer
		handled, code := handle(args, &bytes.Buffer{}, &stderr)
		if !handled || code != 2 || !strings.Contains(stderr.String(), args[1]) {
			t.Errorf("%v: handled=%v code=%d stderr=%q", args, handled, code, stderr.String())
		}
	}
}

func withVersion(t *testing.T, version, build, date, commit string) {
	t.Helper()
	v, b, d, c := Version, Build, BuildDate, Commit
	Version, Build, BuildDate, Commit = version, build, date, commit
	t.Cleanup(func() { Version, Build, BuildDate, Commit = v, b, d, c })
}

func TestStringShowsWhatIsKnown(t *testing.T) {
	cases := []struct {
		version, build, date, commit, want string
	}{
		{"0.1", "57", "2026-10-07T06:38:08Z", "5da629aa28c2", AppName + " 0.1.57 (5da629aa28c2, built 2026-10-07T06:38:08Z)"},
		{"0.1", "", "", "5da629aa28c2", AppName + " 0.1 (5da629aa28c2)"},
		{"0.1", "", "2026-10-07T06:38:08Z", "", AppName + " 0.1 (built 2026-10-07T06:38:08Z)"},
		{"dev", "", "", "", AppName + " dev"},
	}
	for _, c := range cases {
		withVersion(t, c.version, c.build, c.date, c.commit)
		if got := String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, running string
		want            bool
	}{
		{"0.1.58", "0.1.57", true},
		{"0.1.57", "0.1.57", false},
		{"0.1.56", "0.1.57", false},
		{"0.2.1", "0.1.57", true},
		{"0.1.1", "0.1", true}, // a local build of 0.1 is older than any release of it
		{"0.1.1", "dev", true},
		{"1.0.0", "0.9.99", true},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.running); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.running, got, c.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	cases := map[[2]string]string{
		{"linux", "amd64"}:   AppName + "-linux-amd64.zip",
		{"darwin", "arm64"}:  AppName + "-macos-arm64.zip",
		{"windows", "amd64"}: AppName + "-windows-amd64.zip",
	}
	for in, want := range cases {
		if got := AssetName(in[0], in[1]); got != want {
			t.Errorf("AssetName(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestOnPath(t *testing.T) {
	cases := []struct {
		goos, list, dir string
		want            bool
	}{
		{"linux", "/usr/bin:/home/me/.local/bin", "/home/me/.local/bin", true},
		{"linux", "/usr/bin:/home/me/.local/bin/", "/home/me/.local/bin", true},
		{"linux", "/usr/bin:/home/me/.LOCAL/bin", "/home/me/.local/bin", false},
		{"linux", "/usr/bin", "/home/me/.local/bin", false},
		{"linux", "", "/home/me/.local/bin", false},
		{"windows", `C:\Windows;C:\Users\me\.local\bin`, `c:\users\ME\.local\bin`, true},
		{"windows", `C:\Windows;C:\Users\me\.local\bin\`, `C:\Users\me\.local\bin`, true},
		{"windows", `C:\Windows`, `C:\Users\me\.local\bin`, false},
	}
	for _, c := range cases {
		if got := onPath(c.goos, c.list, c.dir); got != c.want {
			t.Errorf("onPath(%q, %q, %q) = %v, want %v", c.goos, c.list, c.dir, got, c.want)
		}
	}
}

func TestInstallIntoCopiesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "src", "tool")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "home", ".local", "bin")

	write := func(content string) {
		if err := os.WriteFile(exe, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("one")
	var out bytes.Buffer
	if err := installInto(&out, exe, binDir); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(binDir, "tool")
	if got, _ := os.ReadFile(dest); string(got) != "one" {
		t.Fatalf("installed %q, want one", got)
	}
	if !strings.Contains(out.String(), "installed") {
		t.Errorf("output %q doesn't say it installed", out.String())
	}

	write("two")
	out.Reset()
	if err := installInto(&out, exe, binDir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "two" {
		t.Fatalf("after upgrading, installed %q, want two", got)
	}
	if !strings.Contains(out.String(), "replaced") {
		t.Errorf("output %q doesn't say it replaced", out.String())
	}
	if info, err := os.Stat(dest); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0) {
		t.Errorf("installed binary isn't executable: %v %v", info, err)
	}
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func releaseServer(t *testing.T, tag, asset string, zipBytes []byte, sum string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/download/"+tag+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		w.Write(zipBytes)
	})
	mux.HandleFunc("/download/"+tag+"/"+sumsFile, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sum + "  " + asset + "\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestTagFollowsTheLatestRedirect(t *testing.T) {
	srv := releaseServer(t, "v0.1.57", "x.zip", nil, "")
	tag, err := latestTag(srv.URL)
	if err != nil || tag != "v0.1.57" {
		t.Fatalf("latestTag = %q, %v", tag, err)
	}
}

func TestDownloadChecksTheChecksum(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{"tool-0.1.1/tool": "binary"})
	h := sha256.Sum256(zipBytes)
	good := hex.EncodeToString(h[:])

	srv := releaseServer(t, "v0.1.1", "tool-linux-amd64.zip", zipBytes, good)
	path, err := download(srv.URL, "v0.1.1", "tool-linux-amd64.zip", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder, err := unzip(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(folder, "tool")); string(got) != "binary" {
		t.Errorf("unpacked %q", got)
	}

	bad := releaseServer(t, "v0.1.1", "tool-linux-amd64.zip", zipBytes, strings.Repeat("0", 64))
	if _, err := download(bad.URL, "v0.1.1", "tool-linux-amd64.zip", t.TempDir()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("a download that doesn't match its checksum was accepted: %v", err)
	}
}

func TestUnzipRefusesToEscapeItsFolder(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{"../evil": "x"})
	zipPath := filepath.Join(t.TempDir(), "evil.zip")
	if err := os.WriteFile(zipPath, zipBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := unzip(zipPath, t.TempDir()); err == nil {
		t.Error("a zip entry outside the target folder was unpacked")
	}
}
