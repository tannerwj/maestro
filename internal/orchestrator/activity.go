package orchestrator

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const runOutputTailBytes = 4096
const runLogMaxBytes = 16 << 20

func (s *Service) markRunActivity(runID string) {
	s.mu.Lock()
	if run := s.activeRunByIDLocked(runID); run != nil {
		run.LastActivityAt = time.Now()
	}
	s.mu.Unlock()
}

type runOutputWriter struct {
	target  io.Writer
	onWrite func()
	append  func([]byte)
}

func (w *runOutputWriter) Write(p []byte) (int, error) {
	n, err := w.target.Write(p)
	if n > 0 {
		if w.append != nil {
			w.append(p[:n])
		}
		if w.onWrite != nil {
			w.onWrite()
		}
	}
	return n, err
}

type runOutputBuffer struct {
	mu        sync.RWMutex
	stdout    tailBuffer
	stderr    tailBuffer
	updatedAt time.Time
}

type tailBuffer struct {
	mu  sync.RWMutex
	buf []byte
}

func (b *tailBuffer) Append(p []byte) {
	if len(p) == 0 {
		return
	}
	if len(p) > runOutputTailBytes {
		p = p[len(p)-runOutputTailBytes:]
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > runOutputTailBytes {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-runOutputTailBytes:]...)
	}
}

func (b *tailBuffer) String() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return string(bytes.Clone(b.buf))
}

func (s *Service) initRunOutput(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runOutputs[runID] = &runOutputBuffer{}
}

func (s *Service) appendRunOutput(runID string, stream string, p []byte) {
	s.mu.RLock()
	output, ok := s.runOutputs[runID]
	s.mu.RUnlock()
	if !ok {
		return
	}
	switch stream {
	case "stderr":
		output.stderr.Append(p)
	default:
		output.stdout.Append(p)
	}
	output.mu.Lock()
	output.updatedAt = time.Now()
	output.mu.Unlock()
}

func (s *Service) clearRunOutput(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runOutputs, runID)
}

func (s *Service) runOutputTails(runID string) (string, string) {
	s.mu.RLock()
	output := s.runOutputs[runID]
	s.mu.RUnlock()
	if output == nil {
		return "", ""
	}
	return output.stdout.String(), output.stderr.String()
}

type boundedRunLog struct {
	file      *os.File
	remaining int64
	truncated bool
}

func (l *boundedRunLog) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	dropped := l.remaining == 0
	if l.remaining > 0 {
		toWrite := int64(len(p))
		if toWrite > l.remaining {
			toWrite = l.remaining
			dropped = true
		}
		n, err := l.file.Write(p[:int(toWrite)])
		if err != nil {
			return n, err
		}
		if n != int(toWrite) {
			return n, io.ErrShortWrite
		}
		l.remaining -= toWrite
	}
	if dropped && !l.truncated {
		if _, err := l.file.WriteString("\n[run log truncated at 16 MiB]\n"); err != nil {
			return 0, err
		}
		l.truncated = true
	}
	return len(p), nil
}

func (l *boundedRunLog) Close() error { return l.file.Close() }

func (s *Service) openRunLog(runID string, name string) (io.WriteCloser, error) {
	dir := filepath.Join(s.stateStore.Dir(), "runs", runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &boundedRunLog{file: file, remaining: runLogMaxBytes}, nil
}
