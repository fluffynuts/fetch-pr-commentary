package appcli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Install copies the running executable into ~/.local/bin, creating that
// folder if need be, then makes sure the folder is on the PATH: on Windows it
// is added to the user's PATH (which only reaches terminals opened from then
// on); elsewhere the PATH is whatever the user's shell profile makes it, so
// all it can do is warn. An older copy is replaced without asking, since
// installing and upgrading is what this is for.
func Install(out io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating the running program: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolving the running program's path: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	binDir := filepath.Join(home, ".local", "bin")
	return installInto(out, exe, binDir)
}

func installInto(out io.Writer, exe, binDir string) error {
	dest := filepath.Join(binDir, filepath.Base(exe))
	if src, err := os.Stat(exe); err == nil {
		if dst, err := os.Stat(dest); err == nil && os.SameFile(src, dst) {
			fmt.Fprintf(out, "%s is already installed at %s\n", AppName, dest)
			return warnIfNotOnPath(out, binDir)
		}
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	_, statErr := os.Stat(dest)
	replacing := statErr == nil
	if err := replaceFile(exe, dest); err != nil {
		return fmt.Errorf("copying %s to %s: %w", filepath.Base(exe), dest, err)
	}
	if replacing {
		fmt.Fprintf(out, "replaced %s\n", dest)
	} else {
		fmt.Fprintf(out, "installed %s\n", dest)
	}
	return warnIfNotOnPath(out, binDir)
}

// replaceFile puts a copy of src at dest, safely even while dest is running
// — an upgrade can't expect every other copy of the program to be closed.
// Writing into a running binary fails ("text file busy" on Linux, a sharing
// violation on Windows), so the copy is written beside dest and renamed into
// place. Windows won't rename onto a running executable either, but it will
// rename one out of the way, so the old binary is moved aside first there;
// it is removed if it can be, and otherwise left as dest+".old" to go the
// next time.
func replaceFile(src, dest string) error {
	tmp := dest + ".new"
	if err := copyFile(src, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err == nil {
		return nil
	}
	old := dest + ".old"
	os.Remove(old)
	if err := os.Rename(dest, old); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Rename(old, dest) // put the old one back
		os.Remove(tmp)
		return err
	}
	os.Remove(old)
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
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
	return os.Chmod(dest, 0o755) // past the umask, so 0755 stays 0755
}

// warnIfNotOnPath makes sure dir, where the program was just installed, is
// on the PATH, telling the user (and never failing) when it isn't.
func warnIfNotOnPath(out io.Writer, dir string) error {
	if onPath(runtime.GOOS, os.Getenv("PATH"), dir) {
		return nil
	}
	if runtime.GOOS != "windows" {
		fmt.Fprintf(out, "WARNING: %s is not on your PATH, so your shell won't find %s yet.\n", dir, AppName)
		fmt.Fprintf(out, "Add it in your shell's profile (~/.profile, ~/.bashrc, ~/.zshrc, config.fish, ...), e.g.:\n")
		fmt.Fprintf(out, "  export PATH=\"%s:$PATH\"\n", dir)
		return nil
	}
	if onPath(runtime.GOOS, persistentPath(), dir) {
		fmt.Fprintf(out, "%s is on your PATH, but only for terminals opened from here on: open a new one to use %s\n", dir, AppName)
		return nil
	}
	added, err := addToUserPath(dir)
	switch {
	case err == nil && added:
		fmt.Fprintf(out, "added %s to your PATH: open a new terminal to use %s\n", dir, AppName)
	case err == nil:
		fmt.Fprintf(out, "%s is on your PATH, but only for terminals opened from here on: open a new one to use %s\n", dir, AppName)
	default:
		fmt.Fprintf(out, "WARNING: %s is not on your PATH, and adding it failed: %s\n", dir, err)
		fmt.Fprintf(out, "Add it for your user, e.g. in PowerShell:\n")
		fmt.Fprintf(out, "  [Environment]::SetEnvironmentVariable('Path', \"$([Environment]::GetEnvironmentVariable('Path', 'User'));%s\", 'User')\n", dir)
		fmt.Fprintf(out, "then open a new terminal.\n")
	}
	return nil
}

// onPath reports whether dir is one of the entries of a PATH-style list for
// the given OS: compared without regard to case or a trailing separator on
// Windows, and exactly elsewhere.
func onPath(goos, list, dir string) bool {
	sep := ":"
	if goos == "windows" {
		sep = ";"
	}
	same := func(a, b string) bool {
		a, b = strings.TrimRight(a, `/\`), strings.TrimRight(b, `/\`)
		if goos == "windows" {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	for _, entry := range strings.Split(list, sep) {
		if entry != "" && same(entry, dir) {
			return true
		}
	}
	return false
}
