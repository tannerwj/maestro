package orchestrator

import (
	"io"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
)

type activityWriter struct {
	target  io.Writer
	onWrite func()
}

func (w *activityWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && w.onWrite != nil {
		w.onWrite()
	}
	return w.target.Write(p)
}

func (s *Service) markRunActivity(runID string) {
	s.updateRun(runID, func(r *domain.AgentRun) {
		r.LastActivityAt = time.Now()
	})
}
