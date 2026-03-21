package orchestrator

import (
	"bytes"
	"io"
	"sync"
	"time"
)

const runOutputTailBytes = 4096

func (s *Service) markRunActivity(runID string) {
	s.mu.Lock()
	if s.activeRun != nil && s.activeRun.ID == runID {
		s.activeRun.LastActivityAt = time.Now()
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
