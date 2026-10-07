//go:build !windows

package appcli

import (
	"errors"
	"os"
)

// persistentPath is the PATH a terminal opened now would start with. Outside
// Windows there's no telling from here what a shell's profile will set, so
// it's this process's own.
func persistentPath() string {
	return os.Getenv("PATH")
}

// addToUserPath is Windows-only: elsewhere the PATH is set by whichever
// shell profile the user keeps, and there's no one right file to add to.
func addToUserPath(string) (added bool, err error) {
	return false, errors.ErrUnsupported
}
