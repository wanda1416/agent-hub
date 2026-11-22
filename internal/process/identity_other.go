//go:build !windows

package process

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// processIdentity returns a fingerprint of a running process so a persisted PID
// can be verified after AgentHub restarts.
func processIdentity(pid int) (ProcessIdentity, bool) {
	if pid <= 0 {
		return ProcessIdentity{}, false
	}

	if runtime.GOOS == "linux" {
		return linuxProcessIdentity(pid)
	}

	// Darwin and the BSDs do not expose a portable creation timestamp without
	// extra dependencies, so the executable path carries the identity there.
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ProcessIdentity{}, false
	}
	exe := strings.TrimSpace(string(out))
	if exe == "" {
		return ProcessIdentity{}, false
	}
	return ProcessIdentity{ExePath: exe}, true
}

func linuxProcessIdentity(pid int) (ProcessIdentity, bool) {
	base := "/proc/" + strconv.Itoa(pid)

	stat, err := os.ReadFile(base + "/stat")
	if err != nil {
		return ProcessIdentity{}, false
	}

	// The comm field can contain spaces and parentheses, so parse from the last
	// ')' instead of splitting the whole line.
	line := string(stat)
	end := strings.LastIndexByte(line, ')')
	if end < 0 || end+2 >= len(line) {
		return ProcessIdentity{}, false
	}
	fields := strings.Fields(line[end+2:])
	// fields[0] is the state (field 3); starttime is field 22.
	var startTime int64
	if len(fields) > 19 {
		startTime, _ = strconv.ParseInt(fields[19], 10, 64)
	}

	identity := ProcessIdentity{StartTime: startTime}
	if exe, err := os.Readlink(base + "/exe"); err == nil {
		identity.ExePath = exe
	}
	return identity, true
}
