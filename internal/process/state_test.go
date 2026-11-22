package process

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestStateStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-state.json")

	store := newStateStore(path)
	want := RuntimeState{
		ID:        "proc-1",
		PID:       4242,
		StartTime: 123456789,
		ExePath:   `C:\services\web.exe`,
		Command:   "web --port 8080",
	}
	if err := store.put(want); err != nil {
		t.Fatalf("put failed: %v", err)
	}

	loaded := newStateStore(path).snapshot()
	got, ok := loaded["proc-1"]
	if !ok {
		t.Fatal("state was not persisted")
	}
	if got.PID != want.PID || got.StartTime != want.StartTime || got.ExePath != want.ExePath || got.Command != want.Command {
		t.Fatalf("loaded state = %+v, want %+v", got, want)
	}

	if err := store.delete("proc-1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if remaining := newStateStore(path).snapshot(); len(remaining) != 0 {
		t.Fatalf("state still persisted after delete: %+v", remaining)
	}
}

func TestAdoptRestoresRunningEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-state.json")
	seed := newStateStore(path)
	if err := seed.put(RuntimeState{
		ID:        "proc-1",
		PID:       4242,
		StartTime: 99,
		ExePath:   `C:\services\web.exe`,
		Command:   "web",
	}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}

	var alive atomic.Bool
	alive.Store(true)

	m := NewManager()
	m.SetStateFile(path)
	m.probe = processProbe{
		alive: func(pid int) bool { return alive.Load() },
		identity: func(pid int) (ProcessIdentity, bool) {
			return ProcessIdentity{StartTime: 99, ExePath: `C:\services\web.exe`}, true
		},
		terminate: func(pid int) { alive.Store(false) },
	}
	m.Register(Definition{ID: "proc-1", Name: "web", Command: "web"})

	m.Adopt(context.Background())

	snap, err := m.Get("proc-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if snap.Status != StatusRunning || snap.PID != 4242 {
		t.Fatalf("adopted entry = status %s pid %d, want running pid 4242", snap.Status, snap.PID)
	}

	// An auto-start after adoption must not spawn a duplicate.
	if err := m.Start(context.Background(), "proc-1"); err != nil {
		t.Fatalf("Start on adopted entry failed: %v", err)
	}

	if err := m.Stop("proc-1"); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	snap, err = m.Get("proc-1")
	if err != nil {
		t.Fatalf("Get after stop failed: %v", err)
	}
	if snap.Status != StatusStopped || snap.PID != 0 {
		t.Fatalf("stopped entry = status %s pid %d, want stopped pid 0", snap.Status, snap.PID)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(newStateStore(path).snapshot()) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("persisted state was not cleared after stop")
}

func TestAdoptRejectsReusedPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-state.json")
	seed := newStateStore(path)
	if err := seed.put(RuntimeState{
		ID:        "proc-1",
		PID:       4242,
		StartTime: 10,
		ExePath:   `C:\services\web.exe`,
	}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}

	m := NewManager()
	m.SetStateFile(path)
	m.probe = processProbe{
		alive: func(pid int) bool { return true },
		identity: func(pid int) (ProcessIdentity, bool) {
			// A different creation time means the PID was reused.
			return ProcessIdentity{StartTime: 20, ExePath: `C:\services\web.exe`}, true
		},
		terminate: func(pid int) {},
	}
	m.Register(Definition{ID: "proc-1", Name: "web", Command: "web"})

	m.Adopt(context.Background())

	snap, err := m.Get("proc-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if snap.Status != StatusStopped || snap.PID != 0 {
		t.Fatalf("reused pid was adopted: status %s pid %d", snap.Status, snap.PID)
	}
	if len(newStateStore(path).snapshot()) != 0 {
		t.Fatal("stale state was not cleared")
	}
}
