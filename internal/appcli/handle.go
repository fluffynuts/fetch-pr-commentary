package appcli

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Handle looks at the command line (os.Args[1:]) for the options in this
// package: -h/--help, -v/--version, -i/--install and -u/--upgrade (which
// also takes -f/--force). When it finds one it does the job and reports
// handled, with the exit code the program should finish with; otherwise the
// arguments are the program's own, and it does nothing.
//
// Only the first argument is looked at, so the program can keep using those
// words further along its own command line.
func Handle(args []string) (handled bool, exitCode int) {
	return handle(args, os.Stdout, os.Stderr)
}

func handle(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	switch args[0] {
	case "-h", "--help":
		WriteHelp(stdout)
		return true, 0
	case "-v", "--version":
		fmt.Fprintln(stdout, String())
		return true, 0
	case "-i", "--install":
		if len(args) > 1 {
			return true, usageError(stderr, args[0], args[1])
		}
		if err := Install(stdout); err != nil {
			fmt.Fprintf(stderr, "%s: %s\n", AppName, err)
			return true, 1
		}
		return true, 0
	case "-u", "--upgrade":
		force := false
		for _, a := range args[1:] {
			if a != "-f" && a != "--force" {
				return true, usageError(stderr, args[0], a)
			}
			force = true
		}
		if err := Upgrade(stdout, force); err != nil {
			fmt.Fprintf(stderr, "%s: %s\n", AppName, err)
			return true, 1
		}
		return true, 0
	}
	return false, 0
}

func usageError(w io.Writer, option, unexpected string) int {
	fmt.Fprintf(w, "%s: %s doesn't take %q (see %s --help)\n", AppName, option, unexpected, AppName)
	return 2
}

// WriteHelp writes the --help text.
func WriteHelp(w io.Writer) {
	fmt.Fprintf(w, "%s %s\n\n", AppName, FullVersion())
	fmt.Fprintf(w, "Usage:\n  %s\n\n", Usage)
	fmt.Fprint(w, "Options:\n")
	fmt.Fprint(w, "  -h, --help       show this help\n")
	fmt.Fprint(w, "  -v, --version    print the version, the commit it was built from and when\n")
	fmt.Fprint(w, "  -i, --install    copy this program to ~/.local/bin, and check that folder is on your PATH\n")
	fmt.Fprint(w, "  -u, --upgrade    download the latest release for this machine and install it\n")
	fmt.Fprint(w, "                   (-f, --force installs it even if it isn't newer than this one)\n")
	if extra := strings.TrimRight(ExtraHelp, "\n"); extra != "" {
		fmt.Fprintln(w, extra)
	}
	if len(Examples) > 0 {
		fmt.Fprint(w, "\nExamples:\n")
		for _, e := range Examples {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
}
