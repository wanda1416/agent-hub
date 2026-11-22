//go:build windows

package process

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// Windows creation flags
const (
	CREATE_NO_WINDOW = 0x08000000
)

// setupProcessGroup sets up process group for Windows
// Creates a new process group for job control and hides the console window
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}

// killProcess kills a process and its children on Windows
func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// On Windows, we use taskkill to kill the process tree
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	err := kill.Run()
	if err != nil {
		// Fallback to direct kill
		return cmd.Process.Kill()
	}
	return nil
}

// gracefulStop attempts to gracefully stop a process on Windows
// Windows doesn't have SIGTERM equivalent, so we send CTRL_BREAK_EVENT
func gracefulStop(cmd *exec.Cmd, done chan struct{}) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// On Windows, we try CTRL_BREAK_EVENT for console apps
	// This may not work for all applications
	dll, err := syscall.LoadDLL("kernel32.dll")
	if err == nil {
		proc, err := dll.FindProc("GenerateConsoleCtrlEvent")
		if err == nil {
			// Send CTRL_BREAK_EVENT (1) to the process group
			_, _, _ = proc.Call(1, uintptr(cmd.Process.Pid))
		}
	}

	// Signal that we've attempted graceful stop
	close(done)
	return nil
}

// terminateProcessID stops an adopted process. It has no exec.Cmd handle - the
// AgentHub that spawned it is gone - so the pid is the only handle left.
func terminateProcessID(pid int) {
	if pid <= 0 {
		return
	}

	// Best effort graceful stop, mirroring gracefulStop for a live child.
	dll, err := syscall.LoadDLL("kernel32.dll")
	if err == nil {
		if proc, err := dll.FindProc("GenerateConsoleCtrlEvent"); err == nil {
			_, _, _ = proc.Call(1, uintptr(pid))
		}
	}

	deadline := time.Now().Add(GracefulStopTimeout)
	for time.Now().Before(deadline) {
		if !isProcessRunning(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		if process, findErr := os.FindProcess(pid); findErr == nil {
			_ = process.Kill()
		}
	}
}

// isProcessRunning checks if a process is still running on Windows.
//
// It must not call os.FindProcess: on Windows that opens a process handle and
// discards it without closing, so polling (adoption, graceful stop) leaked a
// handle per check and kept killed processes openable as zombies.
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	// PROCESS_QUERY_LIMITED_INFORMATION works for more processes than the full
	// query right, including ones running at another integrity level.
	const processQueryLimitedInformation = 0x1000
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// An access denial means the process exists but cannot be queried.
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(handle)

	var exitCode uint32
	err = syscall.GetExitCodeProcess(handle, &exitCode)
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	// STILL_ACTIVE (259) means the process is still running
	return exitCode == 259
}

// getExitCode returns the exit code of a finished process
func getExitCode(cmd *exec.Cmd) int {
	if cmd == nil || cmd.ProcessState == nil {
		return -1
	}
	return cmd.ProcessState.ExitCode()
}
