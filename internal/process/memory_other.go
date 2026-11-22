//go:build !windows

package process

import "fmt"

// memoryUsage is unsupported outside Windows; callers skip such entries.
func memoryUsage(pid int) (uint64, error) {
	return 0, fmt.Errorf("memory usage is not supported on this platform")
}
