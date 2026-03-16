package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tjohnson/maestro/internal/redact"
)

// New creates a JSON logger that writes to stdout and a timestamped file.
func New(level string, dir string, maxFiles int) (*slog.Logger, func() error, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}

	logPath := filepath.Join(dir, time.Now().Format("20060102-150405")+".log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}

	handler := newRedactingHandler(slog.NewJSONHandler(io.MultiWriter(os.Stdout, file), &slog.HandlerOptions{
		Level: parseLevel(level),
	}))

	if err := pruneOldLogs(dir, maxFiles, filepath.Base(logPath)); err != nil {
		_ = file.Close()
		return nil, nil, err
	}

	return slog.New(handler), file.Close, nil
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type redactingHandler struct {
	next slog.Handler
}

func newRedactingHandler(next slog.Handler) slog.Handler {
	return &redactingHandler{next: next}
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clone := slog.NewRecord(record.Time, record.Level, redact.String(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clone.AddAttrs(redactAttr(attr))
		return true
	})
	return h.next.Handle(ctx, clone)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sanitized := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		sanitized = append(sanitized, redactAttr(attr))
	}
	return &redactingHandler{next: h.next.WithAttrs(sanitized)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

func redactAttr(attr slog.Attr) slog.Attr {
	attr.Value = redactValue(attr.Value)
	return attr
}

func redactValue(value slog.Value) slog.Value {
	switch value.Kind() {
	case slog.KindString:
		return slog.StringValue(redact.String(value.String()))
	case slog.KindGroup:
		attrs := value.Group()
		sanitized := make([]slog.Attr, 0, len(attrs))
		for _, attr := range attrs {
			sanitized = append(sanitized, redactAttr(attr))
		}
		return slog.GroupValue(sanitized...)
	case slog.KindAny:
		if err, ok := value.Any().(error); ok {
			return slog.StringValue(redact.String(err.Error()))
		}
	}
	return value
}

func pruneOldLogs(dir string, maxFiles int, keep string) error {
	if maxFiles <= 0 {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	type logEntry struct {
		name    string
		modTime time.Time
	}

	logs := make([]logEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		logs = append(logs, logEntry{name: entry.Name(), modTime: info.ModTime()})
	}

	if len(logs) <= maxFiles {
		return nil
	}

	sort.Slice(logs, func(i, j int) bool {
		if logs[i].modTime.Equal(logs[j].modTime) {
			return logs[i].name < logs[j].name
		}
		return logs[i].modTime.Before(logs[j].modTime)
	})

	toRemove := len(logs) - maxFiles
	for _, entry := range logs {
		if toRemove == 0 {
			break
		}
		if entry.name == keep {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old log %s: %w", entry.name, err)
		}
		toRemove--
	}
	return nil
}
