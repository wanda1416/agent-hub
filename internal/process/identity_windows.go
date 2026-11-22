//go:build windows

package process

import (
	"golang.org/x/sys/windows"
)

// processIdentity returns the creation time and executable path of a running
// process. Both are used to verify that a persisted PID still belongs to the
// process recorded before AgentHub restarted.
func processIdentity(pid int) (ProcessIdentity, bool) {
	if pid <= 0 {
		return ProcessIdentity{}, false
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Older protected processes may reject the limited handle; the full
		// query right is the historical fallback.
		handle, err = windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
		if err != nil {
			return ProcessIdentity{}, false
		}
	}
	defer windows.CloseHandle(handle)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return ProcessIdentity{}, false
	}

	identity := ProcessIdentity{
		StartTime: int64(uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)),
	}

	size := uint32(windows.MAX_PATH)
	buf := make([]uint16, size)
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err == nil && size > 0 {
		identity.ExePath = windows.UTF16ToString(buf[:size])
	}

	return identity, true
}
