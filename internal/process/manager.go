package process

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agenthub/internal/platform"
)

const (
	// GracefulStopTimeout is the time to wait for graceful process termination
	GracefulStopTimeout = 5 * time.Second
	// retryCooldown is the pause between an automatic restart and the next
	// spawn attempt.
	retryCooldown = 2 * time.Second
	// retryPollInterval is how often the cooldown re-checks for a stop request,
	// so a stop during the cooldown takes effect promptly.
	retryPollInterval = 50 * time.Millisecond
	// loopExitTimeout bounds how long Stop waits for a run loop to end.
	loopExitTimeout = 5 * time.Second
	// adoptPollInterval is how often an adopted process is checked for exit.
	adoptPollInterval = 500 * time.Millisecond
)

var (
	ErrNotFound = errors.New("process not found")
)

// LogCallback is called when process outputs data
type LogCallback func(processID, stream, line string)

// LifecycleAction identifies a process lifecycle transition.
type LifecycleAction string

const (
	// ActionSpawn is emitted whenever a child process is started (successfully
	// or not).
	ActionSpawn LifecycleAction = "spawn"
	// ActionExit is emitted when a child process has exited.
	ActionExit LifecycleAction = "exit"
	// ActionRestart is emitted when the run loop schedules an automatic retry.
	ActionRestart LifecycleAction = "restart"
	// ActionStop is emitted when a stop is requested for an entry.
	ActionStop LifecycleAction = "stop"
)

// LifecycleEvent records one process lifecycle transition for auditing. Without
// it "one entry ended up with two children" can only be reconstructed from the
// OS process list, which loses the trigger and the owning entry.
type LifecycleEvent struct {
	Action  LifecycleAction
	ID      string
	PID     int
	Command string
	Reason  string
}

// LifecycleCallback receives lifecycle events for audit logging.
type LifecycleCallback func(LifecycleEvent)

type Manager struct {
	mu                sync.RWMutex
	entries           map[string]*entry
	logCallback       LogCallback
	lifecycleCallback LifecycleCallback
	// state persists the pid identity of every spawned child so a restarted
	// AgentHub can re-adopt it. It is nil when persistence is disabled (tests).
	state *stateStore
	// probe abstracts the OS calls used to verify and stop adopted processes,
	// so adoption can be tested without real child processes.
	probe processProbe
	// streamDir stores durable child stdout/stderr files. Children write to these
	// files directly, so their output stays valid after AgentHub exits (an
	// anonymous pipe would break and crash writers such as Node on EPIPE).
	streamDir string
}

type processProbe struct {
	identity  func(pid int) (ProcessIdentity, bool)
	alive     func(pid int) bool
	terminate func(pid int)
}

type entry struct {
	definition Definition
	restarts   int
	status     Status
	cmd        *exec.Cmd
	// pid is the process id of the current child, published only after the
	// child started successfully and cleared when the loop ends. It is kept
	// separate from cmd because cmd.Process is written by exec.Cmd.Start() and
	// reading it from another goroutine (List/Get/Stop) would race with Start.
	pid             int
	lastError       string
	manuallyStopped bool // true when stopped by user, false when stopped automatically
	// loopActive is true while a run loop is alive for this entry. It is the
	// authoritative "is this entry started" flag: status is not usable for that
	// because it also reflects non-running states (retry cooldown, errored)
	// during which the loop is still alive and will spawn again.
	loopActive bool
	// adopted is true while the entry represents a child inherited from a
	// previous AgentHub run. Such an entry has no exec.Cmd; it is watched by pid
	// and stopped with terminateProcessID.
	adopted bool
}

func NewManager() *Manager {
	return &Manager{
		entries: make(map[string]*entry),
		probe: processProbe{
			identity:  processIdentity,
			alive:     isProcessRunning,
			terminate: terminateProcessID,
		},
	}
}

// SetStateFile enables runtime-state persistence at path. It must be called
// before Adopt so persisted children can be restored on startup.
func (m *Manager) SetStateFile(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = newStateStore(path)
}

// SetStreamDir sets where child stdout/stderr files are kept. It must be called
// before any process starts.
func (m *Manager) SetStreamDir(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamDir = path
}

// SetLogCallback sets the callback function for process logs
func (m *Manager) SetLogCallback(cb LogCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logCallback = cb
}

// SetLifecycleCallback sets the callback function for lifecycle audit events.
func (m *Manager) SetLifecycleCallback(cb LifecycleCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lifecycleCallback = cb
}

func (m *Manager) emitLifecycle(ev LifecycleEvent) {
	m.mu.RLock()
	cb := m.lifecycleCallback
	m.mu.RUnlock()
	if cb != nil {
		cb(ev)
	}
}

func (m *Manager) List() []Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshots := make([]Snapshot, 0, len(m.entries))
	for _, item := range m.entries {
		snapshots = append(snapshots, Snapshot{
			Definition: item.definition,
			PID:        item.pid,
			Status:     item.status,
			Restarts:   item.restarts,
			LastError:  item.lastError,
		})
	}
	return snapshots
}

// MemoryUsage samples the working set of every running process. It is meant
// for on-demand refresh only: reading process counters is more expensive than
// the status list, so the periodic poll does not call it.
func (m *Manager) MemoryUsage() []MemoryUsage {
	m.mu.RLock()
	pids := make(map[string]int, len(m.entries))
	for id, item := range m.entries {
		if item.pid > 0 {
			pids[id] = item.pid
		}
	}
	m.mu.RUnlock()

	usages := make([]MemoryUsage, 0, len(pids))
	for id, pid := range pids {
		bytes, err := memoryUsage(pid)
		if err != nil {
			continue
		}
		usages = append(usages, MemoryUsage{ID: id, PID: pid, Bytes: bytes})
	}
	return usages
}

func (m *Manager) Register(def Definition) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries[def.ID] = &entry{
		definition: def,
		status:     StatusStopped,
	}
}

// UpdateDefinition replaces the stored definition of an existing entry without
// touching its runtime state. A running process keeps running and uses the new
// command only after its next start or restart, which makes it safe for bulk
// operations such as importing configuration over live processes.
func (m *Manager) UpdateDefinition(id string, def Definition) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	item, ok := m.entries[id]
	if !ok {
		return ErrNotFound
	}
	// The entry is keyed by ID; never let a caller move it under another key.
	def.ID = id
	item.definition = def
	return nil
}

// SetAutoStart updates the auto-start flag of a process without disturbing a
// running process (it does not stop or restart the process).
func (m *Manager) SetAutoStart(id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	item, ok := m.entries[id]
	if !ok {
		return ErrNotFound
	}
	item.definition.AutoStart = enabled
	return nil
}

// Unregister removes a process from the manager
func (m *Manager) Unregister(id string) error {
	m.mu.Lock()
	item, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}

	// Stop the process if a run loop is still alive.
	if item.loopActive {
		m.mu.Unlock()
		_ = m.Stop(id)
		m.mu.Lock()
	}

	delete(m.entries, id)
	m.mu.Unlock()
	return nil
}

// Get returns a snapshot for a specific process
func (m *Manager) Get(id string) (Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	item, ok := m.entries[id]
	if !ok {
		return Snapshot{}, ErrNotFound
	}

	return Snapshot{
		Definition: item.definition,
		PID:        item.pid,
		Status:     item.status,
		Restarts:   item.restarts,
		LastError:  item.lastError,
	}, nil
}

// ActiveNames returns the names of processes whose run loop is alive. It covers
// entries in retry cooldown too, which would spawn again moments later, so
// update guards must use this.
func (m *Manager) ActiveNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.entries))
	for _, item := range m.entries {
		if item.loopActive {
			name := item.definition.Name
			if name == "" {
				name = item.definition.ID
			}
			names = append(names, name)
		}
	}
	return names
}

func (m *Manager) Start(ctx context.Context, id string) error {
	m.mu.Lock()
	item, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}

	// The run loop is the only thing that spawns children, so the entry is
	// already started whenever its loop is alive - regardless of the status
	// field, which also holds non-running values (retry cooldown, errored)
	// while the loop is still alive and will spawn again. Guarding on status
	// alone let a second Start launch a second loop (and a second child).
	if item.loopActive {
		m.mu.Unlock()
		return nil
	}

	item.loopActive = true
	item.manuallyStopped = false // Reset manual stop flag when starting
	item.status = StatusStarting
	m.mu.Unlock()

	go m.run(ctx, id)
	return nil
}

func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	item, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}

	item.status = StatusStopped
	item.manuallyStopped = true // Mark as manually stopped to prevent auto-restart
	cmd := item.cmd
	pid := item.pid
	adopted := item.adopted
	def := item.definition
	m.mu.Unlock()

	m.emitLifecycle(LifecycleEvent{
		Action:  ActionStop,
		ID:      id,
		PID:     pid,
		Command: commandLine(def),
		Reason:  "manual",
	})

	// pid > 0 means the child had already started, so cmd.Process is published
	// and safe to read here. When it is still 0 the loop has not finished
	// starting the child yet; the loop itself kills it (see run).
	if pid > 0 {
		if cmd != nil {
			terminate(cmd)
		} else if adopted {
			// The child was inherited from a previous AgentHub run, so there is
			// no exec.Cmd left; stop it by pid instead.
			m.probe.terminate(pid)
		}
	}

	// Wait for the run loop to end. Without this a stop during the retry
	// cooldown would be undone by the loop, and a following Start (as done by
	// RestartProcess) could race with the loop it is replacing.
	m.waitLoopExit(id)

	// A stop always ends the persisted state for the entry. If the child could
	// not actually be stopped, keep the record so the next start can adopt it
	// rather than spawning a duplicate into the same port.
	if m.state != nil && (pid <= 0 || m.probe.alive == nil || !m.probe.alive(pid)) {
		_ = m.state.delete(id)
	}
	return nil
}

// terminate stops a running command: it signals the process tree gracefully and
// force-kills it once the graceful timeout elapses. It returns after the command
// is gone (or after the timeout, if the process cannot be observed as stopped).
func terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	done := make(chan struct{})
	go func() {
		_ = gracefulStop(cmd, done)
	}()

	select {
	case <-done:
	case <-time.After(GracefulStopTimeout):
		_ = killProcess(cmd)
		return
	}

	// The graceful signal was sent; give the process a moment to exit before
	// force-killing it.
	deadline := time.Now().Add(GracefulStopTimeout)
	for time.Now().Before(deadline) {
		if !isProcessRunning(cmd.Process.Pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = killProcess(cmd)
}

// waitLoopExit blocks until the entry's run loop has ended, or until the timeout
// elapses. Callers use it after asking a process to stop so the loop cannot
// interfere with a following start.
func (m *Manager) waitLoopExit(id string) {
	deadline := time.Now().Add(loopExitTimeout)
	for {
		m.mu.RLock()
		item, ok := m.entries[id]
		active := ok && item.loopActive
		m.mu.RUnlock()
		if !active || time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// finishLoop clears the run-loop bookkeeping of an entry. It runs exactly once
// per loop, on every return path, so a concurrent Start sees the entry as
// stopped only after the previous loop has really ended.
func (m *Manager) finishLoop(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.entries[id]
	if !ok {
		return
	}
	item.loopActive = false
	item.adopted = false
	item.cmd = nil
	item.pid = 0
	if m.state != nil {
		_ = m.state.delete(id)
	}
}

// Adopt restores children that survived a previous AgentHub run. A persisted
// pid is only claimed when its creation time and executable path still match,
// so a reused pid is never mistaken for a managed process. Adopted entries get
// a watcher goroutine and are reported as running; Start on them is a no-op.
func (m *Manager) Adopt(ctx context.Context) {
	if m.state == nil {
		return
	}

	for id, state := range m.state.snapshot() {
		m.mu.Lock()
		item, ok := m.entries[id]
		if !ok || item.loopActive || state.PID <= 0 {
			m.mu.Unlock()
			_ = m.state.delete(id)
			continue
		}
		if !m.verifyState(state) {
			m.mu.Unlock()
			_ = m.state.delete(id)
			continue
		}

		item.pid = state.PID
		item.adopted = true
		item.loopActive = true
		item.manuallyStopped = false
		item.status = StatusRunning
		command := state.Command
		m.mu.Unlock()

		m.emitLifecycle(LifecycleEvent{
			Action:  ActionSpawn,
			ID:      id,
			PID:     state.PID,
			Command: command,
			Reason:  "adopt",
		})

		go m.watchAdopted(ctx, id, state)
	}
}

// verifyState reports whether a persisted pid still belongs to the process it
// was recorded for. When the probe cannot read the identity, the pid is not
// claimed: spawning a duplicate is safer than stopping an unrelated process.
func (m *Manager) verifyState(state RuntimeState) bool {
	if state.PID <= 0 || m.probe.alive == nil || !m.probe.alive(state.PID) {
		return false
	}
	if m.probe.identity == nil {
		return false
	}
	current, ok := m.probe.identity(state.PID)
	if !ok {
		return false
	}
	if state.StartTime != 0 && current.StartTime != 0 && state.StartTime != current.StartTime {
		return false
	}
	if state.ExePath != "" && current.ExePath != "" &&
		!strings.EqualFold(filepath.Clean(state.ExePath), filepath.Clean(current.ExePath)) {
		return false
	}
	return true
}

// watchAdopted polls an inherited child until it exits. It never spawns a
// replacement: the restart policy of the previous run cannot be applied safely
// without an exit code, and silently starting a second copy is the failure this
// adoption is meant to prevent.
func (m *Manager) watchAdopted(ctx context.Context, id string, state RuntimeState) {
	logCb := m.logCallback
	var tailStop chan struct{}
	var tailWG sync.WaitGroup
	if logCb != nil && m.streamDir != "" {
		tailStop = make(chan struct{})
		tailWG.Add(2)
		for _, stream := range []string{"stdout", "stderr"} {
			stream := stream
			go func() {
				defer tailWG.Done()
				tailStream(id, stream, m.streamPathDir(id, stream), true, tailStop, logCb)
			}()
		}
	}
	defer func() {
		if tailStop != nil {
			close(tailStop)
			tailWG.Wait()
		}
	}()

	ticker := time.NewTicker(adoptPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// The app is closing: leave the child running and keep its state so
			// the next start can adopt it again.
			return
		case <-ticker.C:
		}

		if m.verifyState(state) {
			continue
		}

		m.mu.Lock()
		item, ok := m.entries[id]
		if !ok || !item.adopted || item.pid != state.PID {
			m.mu.Unlock()
			return
		}
		item.adopted = false
		item.loopActive = false
		item.pid = 0
		item.status = StatusStopped
		m.mu.Unlock()

		if m.state != nil {
			_ = m.state.delete(id)
		}
		m.emitLifecycle(LifecycleEvent{
			Action:  ActionExit,
			ID:      id,
			PID:     state.PID,
			Command: state.Command,
			Reason:  "exited while adopted",
		})
		return
	}
}

// saveRuntimeState records the identity of a freshly spawned child. Failures are
// best effort: the process keeps running, it just cannot be adopted after a
// restart.
func (m *Manager) saveRuntimeState(id string, pid int, command string) {
	if m.state == nil || pid <= 0 {
		return
	}

	state := RuntimeState{
		ID:        id,
		PID:       pid,
		Command:   command,
		UpdatedAt: time.Now(),
	}
	if m.probe.identity != nil {
		if identity, ok := m.probe.identity(pid); ok {
			state.StartTime = identity.StartTime
			state.ExePath = identity.ExePath
		}
	}
	_ = m.state.put(state)
}

func (m *Manager) setStatus(id string, status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item, ok := m.entries[id]; ok {
		item.status = status
	}
}

func (m *Manager) run(ctx context.Context, id string) {
	// The loop guard is cleared once, on every return path, so Start can rely on
	// it to know whether a loop is still alive.
	defer m.finishLoop(id)

	for {
		m.mu.Lock()
		item, ok := m.entries[id]
		if !ok {
			m.mu.Unlock()
			return
		}

		// A stop requested while the loop was between attempts must end the loop
		// instead of spawning the process again.
		if item.manuallyStopped {
			item.status = StatusStopped
			m.mu.Unlock()
			return
		}

		// Clear previous error on restart
		item.lastError = ""
		reason := "start"
		if item.restarts > 0 {
			reason = "retry"
		}
		policy := item.definition.RestartPolicy
		command := commandLine(item.definition)

		cmd := exec.CommandContext(ctx, item.definition.Command, item.definition.Args...)
		cmd.Dir = item.definition.WorkingDir
		cmd.Env = append(cmd.Env, os.Environ()...)
		cmd.Env = append(cmd.Env, envFromMap(item.definition.Env)...)

		// Set up platform-specific process group for proper child process handling
		setupProcessGroup(cmd)

		// Point stdout/stderr at durable files when possible. A child that keeps a
		// file handle is unaffected by AgentHub exiting; an anonymous pipe would be
		// closed with the app and writers such as Node would die on EPIPE.
		streams := m.openChildStreams(id)
		var stdoutPipe, stderrPipe io.ReadCloser
		if streams.ok {
			cmd.Stdout = streams.stdoutFile
			cmd.Stderr = streams.stderrFile
		} else {
			stdoutPipe, _ = cmd.StdoutPipe()
			stderrPipe, _ = cmd.StderrPipe()
		}

		item.cmd = cmd
		item.pid = 0
		item.status = StatusStarting
		logCb := m.logCallback
		m.mu.Unlock()

		err := cmd.Start()
		if err != nil {
			streams.close()
			m.recordError(id, err)
			m.emitLifecycle(LifecycleEvent{
				Action: ActionSpawn, ID: id, Command: command, Reason: "failed: " + err.Error(),
			})
			if !m.shouldRestart(id) {
				return
			}
			if !m.waitForRetry(id) {
				return
			}
			continue
		}

		// Publish the child pid under the lock, which also makes cmd.Process safe
		// to read from Stop. A stop (or removal) that arrived while the child was
		// starting kills it here instead, and the wait path below reaps it, so a
		// stop can never be undone by a child it did not get to see.
		childPID := cmd.Process.Pid
		m.mu.Lock()
		current, ok := m.entries[id]
		stopped := !ok || current.manuallyStopped
		if !stopped {
			current.pid = childPID
			current.status = StatusRunning
		}
		m.mu.Unlock()

		if stopped {
			_ = killProcess(cmd)
		}

		m.emitLifecycle(LifecycleEvent{
			Action: ActionSpawn, ID: id, PID: childPID, Command: command, Reason: reason,
		})

		// Persist the child identity so a restarted AgentHub can adopt it instead
		// of spawning a duplicate.
		if !stopped {
			m.saveRuntimeState(id, childPID, command)
		}

		var tailStop chan struct{}
		var tailWG sync.WaitGroup
		if logCb != nil {
			if streams.ok {
				// The child inherited its own handles, so the parent copies can be
				// closed now; the tailers read the same files by path.
				streams.close()
				tailStop = make(chan struct{})
				tailWG.Add(2)
				go func() {
					defer tailWG.Done()
					tailStream(id, "stdout", streams.stdoutDir, false, tailStop, logCb)
				}()
				go func() {
					defer tailWG.Done()
					tailStream(id, "stderr", streams.stderrDir, false, tailStop, logCb)
				}()
			} else {
				// Stream stdout/stderr from pipes when the files could not be opened.
				if stdoutPipe != nil {
					go m.streamOutput(id, "stdout", stdoutPipe, logCb)
				}
				if stderrPipe != nil {
					go m.streamOutput(id, "stderr", stderrPipe, logCb)
				}
			}
		} else {
			streams.close()
		}

		err = cmd.Wait()
		if tailStop != nil {
			close(tailStop)
			tailWG.Wait()
		}
		if err != nil {
			m.recordError(id, err)
		}

		// Re-read the manual-stop flag after the wait: a stop (or removal) that
		// arrived while the process was exiting must end the loop.
		m.mu.Lock()
		current, ok = m.entries[id]
		stopped = !ok || current.manuallyStopped
		m.mu.Unlock()

		exitReason := "exit 0"
		if err != nil {
			exitReason = err.Error()
		}
		m.emitLifecycle(LifecycleEvent{
			Action: ActionExit, ID: id, PID: childPID, Command: command, Reason: exitReason,
		})

		if stopped {
			m.setStatus(id, StatusStopped)
			return
		}

		if !m.shouldRestart(id) {
			return
		}
		if policy == "" {
			policy = RestartOnFailure
		}
		m.emitLifecycle(LifecycleEvent{
			Action: ActionRestart, ID: id, PID: childPID, Command: command,
			Reason: "policy=" + string(policy),
		})
		if !m.waitForRetry(id) {
			return
		}
	}
}

func (m *Manager) streamOutput(id, stream string, reader io.Reader, callback LogCallback) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		// Console programs on Windows emit legacy-encoded bytes when their
		// output is piped, so decode to UTF-8 before the line is stored.
		line := platform.DecodeOutput(scanner.Bytes())
		if callback != nil {
			callback(id, stream, line)
		}
	}
}

func (m *Manager) shouldRestart(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	item, ok := m.entries[id]
	if !ok {
		return false
	}

	policy := item.definition.RestartPolicy
	if policy == "" {
		policy = RestartOnFailure
	}

	if policy == RestartNever {
		item.status = StatusStopped
		return false
	}

	if policy == RestartOnFailure && item.lastError == "" {
		item.status = StatusStopped
		return false
	}

	item.restarts++
	if item.definition.MaxRetries > 0 && item.restarts > item.definition.MaxRetries {
		item.status = StatusErrored
		return false
	}

	return true
}

// waitForRetry waits out the restart cooldown. It returns false when the entry
// disappeared or a stop was requested while waiting, so the caller ends the loop
// instead of spawning again.
func (m *Manager) waitForRetry(id string) bool {
	m.mu.Lock()
	item, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return false
	}
	if item.manuallyStopped {
		m.mu.Unlock()
		return false
	}
	// The entry is waiting to retry, not failed: report it as starting so the UI
	// offers "stop" instead of "start" while a child is imminent.
	item.status = StatusStarting
	m.mu.Unlock()

	deadline := time.Now().Add(retryCooldown)
	for {
		m.mu.RLock()
		current, ok := m.entries[id]
		stopped := !ok || current.manuallyStopped
		m.mu.RUnlock()
		if stopped {
			return false
		}
		if !time.Now().Before(deadline) {
			return true
		}
		time.Sleep(retryPollInterval)
	}
}

// commandLine renders a definition's command with its arguments for audit logs.
func commandLine(def Definition) string {
	if len(def.Args) == 0 {
		return def.Command
	}
	return def.Command + " " + strings.Join(def.Args, " ")
}

func (m *Manager) recordError(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	item, ok := m.entries[id]
	if !ok {
		return
	}
	item.lastError = err.Error()
	item.status = StatusErrored
}

func envFromMap(extra Environment) []string {
	output := make([]string, 0, len(extra))
	for key, value := range extra {
		output = append(output, key+"="+value)
	}
	return output
}
