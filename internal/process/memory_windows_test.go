//go:build windows

package process

import (
	"os"
	"testing"
)

func TestMemoryUsageCurrentProcess(t *testing.T) {
	bytes, err := memoryUsage(os.Getpid())
	if err != nil {
		t.Fatalf("memoryUsage: %v", err)
	}
	if bytes < 1024*1024 {
		t.Fatalf("suspiciously small working set: %d", bytes)
	}
	t.Logf("working set: %.1f MB", float64(bytes)/1024/1024)
}
