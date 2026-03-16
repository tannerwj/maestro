package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedactingHandlerRedactsMessagesAndAttrs(t *testing.T) {
	var buf bytes.Buffer
	handler := newRedactingHandler(slog.NewJSONHandler(&buf, nil))
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "using glpat-secret", 0)
	record.AddAttrs(
		slog.String("url", "https://oauth2:secret@example.com/repo.git"),
		slog.String("auth", "Authorization: Basic abc123"),
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("handle record: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("decode log payload: %v", err)
	}

	raw := buf.String()
	for _, fragment := range []string{"secret", "abc123"} {
		if strings.Contains(raw, fragment) {
			t.Fatalf("log leaked %q in %q", fragment, raw)
		}
	}
}

func TestNewPrunesOldLogs(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		path := filepath.Join(dir, time.Now().Add(time.Duration(i)*time.Second).Format("20060102-150405")+".log")
		if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
			t.Fatalf("write old log: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	logger, closeFn, err := New("info", dir, 3)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	logger.Info("hello")
	if err := closeFn(); err != nil {
		t.Fatalf("close logger: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".log") {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("log count = %d, want 3", count)
	}
}
