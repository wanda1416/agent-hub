//go:build windows

package platform

import (
	"errors"

	"golang.org/x/sys/windows"
)

// ProcessAlive reports whether a process with the given PID is still running.
// A PID that cannot be opened because of an access denial is treated as alive:
// an instance running under a different integrity level still exists, and
// reporting it dead would let a duplicate launch start.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(handle)

	// A terminated process can remain openable until the last handle is closed;
	// its exit code is what distinguishes it from a running one.
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	return exitCode == 259 // STILL_ACTIVE
}
