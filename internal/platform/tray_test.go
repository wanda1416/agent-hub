package platform

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTrayApp records the calls TrayManager makes into the main App.
type fakeTrayApp struct {
	mu       sync.Mutex
	locale   string
	ctx      context.Context
	prepared bool
	logs     []string
}

func (f *fakeTrayApp) GetLocale() string       { return f.locale }
func (f *fakeTrayApp) GetCtx() context.Context { return f.ctx }

func (f *fakeTrayApp) DescribeProcesses() string {
	return "processes: web(id-1) status=stopped pid=0 restarts=0"
}

func (f *fakeTrayApp) PrepareQuit() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepared = true
}

func (f *fakeTrayApp) LogSystemError(component, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, component+": "+message)
}

func (f *fakeTrayApp) hasLog(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, line := range f.logs {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// waitForLog polls until a matching line appears: tray logging is asynchronous
// so that a hung log write can never stall the tray menu.
func (f *fakeTrayApp) waitForLog(substr string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		if f.hasLog(substr) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// logIndex returns the position of the first matching line, -1 when absent.
func (f *fakeTrayApp) logIndex(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, line := range f.logs {
		if strings.Contains(line, substr) {
			return i
		}
	}
	return -1
}

// stubSystrayQuit replaces the tray shutdown with a no-op: the test never starts
// the real systray loop, and Quit panics without it.
func stubSystrayQuit(t *testing.T) {
	t.Helper()
	original := systrayQuit
	systrayQuit = func() {}
	t.Cleanup(func() { systrayQuit = original })
}

// TestQuitAppLogsRequest pins the diagnostic line written on every tray quit,
// which is what identified the quit path in user reports.
func TestQuitAppLogsRequest(t *testing.T) {
	stubSystrayQuit(t)
	app := &fakeTrayApp{}
	tray := NewTrayManager(app, nil, nil)

	tray.quitApp()

	if !app.waitForLog("Quit requested from tray menu") {
		t.Error("want a quit-request line in the tray log")
	}
	if !app.prepared {
		t.Error("quitApp did not mark the app as quitting")
	}
}

// TestQuitLogOrder pins that the quit diagnostics land in the order the steps
// ran, so a user's log file reads as a timeline instead of a shuffled set.
func TestQuitLogOrder(t *testing.T) {
	stubSystrayQuit(t)
	app := &fakeTrayApp{}
	tray := NewTrayManager(app, nil, nil)

	tray.quitApp()

	if !app.waitForLog("Quit requested") {
		t.Fatal("want a quit-request line in the tray log")
	}
	request := app.logIndex("Quit requested")
	prepare := app.logIndex("app marked as quitting")
	if request < 0 || prepare < 0 {
		t.Fatalf("missing quit lines: request=%d prepare=%d", request, prepare)
	}
	if request > prepare {
		t.Errorf("quit log order = request %d, prepare %d; want ascending", request, prepare)
	}
}

func TestTrayLabels(t *testing.T) {
	// Keep the system-locale fallback out of the way so only the app locale
	// decides the labels.
	for _, env := range []string{"LANG", "LC_ALL", "LC_MESSAGES", "LANGUAGE"} {
		t.Setenv(env, "")
	}
	tests := []struct {
		locale string
		show   string
		quit   string
	}{
		{locale: "zh", show: "显示界面", quit: "退出"},
		{locale: "en", show: "Show Window", quit: "Quit"},
	}
	for _, tc := range tests {
		app := &fakeTrayApp{locale: tc.locale}
		tray := NewTrayManager(app, nil, nil)
		show, quit := tray.getLocalizedLabels()
		if show != tc.show || quit != tc.quit {
			t.Errorf("locale %q labels = %q / %q, want %q / %q", tc.locale, show, quit, tc.show, tc.quit)
		}
	}
}

// TestLogSystemWithoutApp guards the nil-app path used before Startup wired the
// App into the tray manager.
func TestLogSystemWithoutApp(t *testing.T) {
	tray := NewTrayManager(nil, nil, nil)
	tray.logSystem("no app yet")
}
