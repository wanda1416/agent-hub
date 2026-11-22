package process

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agenthub/internal/logging"
)

const (
	// streamPollInterval is how often the tailer checks child output files.
	streamPollInterval = 200 * time.Millisecond
	// streamKeepFiles bounds how many output files are kept per stream.
	streamKeepFiles = 2
)

// childStreams holds the parent-side handles and directories for one spawn.
// Children write straight to these files, so their stdout/stderr survives an
// AgentHub exit instead of breaking with the old pipe.
type childStreams struct {
	stdoutFile *os.File
	stderrFile *os.File
	stdoutDir  string
	stderrDir  string
	ok         bool
}

func (s childStreams) close() {
	if s.stdoutFile != nil {
		_ = s.stdoutFile.Close()
	}
	if s.stderrFile != nil {
		_ = s.stderrFile.Close()
	}
}

// openChildStreams creates the files a new child will write to. Each spawn gets
// its own file, and older files are pruned so the directory stays small.
func (m *Manager) openChildStreams(id string) childStreams {
	if m.streamDir == "" {
		return childStreams{}
	}

	stdoutDir := m.streamPathDir(id, "stdout")
	stderrDir := m.streamPathDir(id, "stderr")
	if err := os.MkdirAll(stdoutDir, 0o755); err != nil {
		return childStreams{}
	}
	if err := os.MkdirAll(stderrDir, 0o755); err != nil {
		return childStreams{}
	}

	stdoutFile, err := createStreamFile(stdoutDir)
	if err != nil {
		return childStreams{}
	}
	stderrFile, err := createStreamFile(stderrDir)
	if err != nil {
		_ = stdoutFile.Close()
		return childStreams{}
	}

	return childStreams{
		stdoutFile: stdoutFile,
		stderrFile: stderrFile,
		stdoutDir:  stdoutDir,
		stderrDir:  stderrDir,
		ok:         true,
	}
}

func (m *Manager) streamPathDir(id, stream string) string {
	return filepath.Join(m.streamDir, id, stream)
}

func createStreamFile(dir string) (*os.File, error) {
	name := time.Now().Format("20060102_150405.000") + ".log"
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	pruneStreamDir(dir)
	return file, nil
}

// pruneStreamDir keeps only the newest streamKeepFiles files. Files belong to
// finished spawns, so removing them never touches the running child's handle.
func pruneStreamDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= streamKeepFiles {
		return
	}

	type candidate struct {
		path string
		mod  time.Time
	}
	files := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, candidate{path: filepath.Join(dir, entry.Name()), mod: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })

	for i := streamKeepFiles; i < len(files); i++ {
		_ = os.Remove(files[i].path)
	}
}

// tailStream forwards complete lines appended to the newest file under dir.
// fromEnd skips output written before adoption, so a restarted AgentHub resumes
// where the previous run stopped reading.
func tailStream(id, stream, dir string, fromEnd bool, stop <-chan struct{}, cb LogCallback) {
	if cb == nil {
		return
	}
	tailer := logging.NewTailer(dir)
	if fromEnd {
		_ = tailer.SeekEnd()
	}

	ticker := time.NewTicker(streamPollInterval)
	defer ticker.Stop()

	drain := func() {
		// A few passes catch lines that arrived while the previous batch was
		// being delivered.
		for i := 0; i < 4; i++ {
			lines, err := tailer.Next()
			if err != nil || len(lines) == 0 {
				return
			}
			for _, line := range lines {
				cb(id, stream, strings.TrimSuffix(line, "\r"))
			}
		}
	}

	drain()
	for {
		select {
		case <-stop:
			drain()
			return
		case <-ticker.C:
			drain()
		}
	}
}
