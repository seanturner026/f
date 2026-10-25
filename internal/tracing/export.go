package tracing

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// fileExporter buffers spans and appends them as one OTLP/JSON line each time a
// root span ends (one line per f run; per poll in long-running f prs --watch).
type fileExporter struct {
	path string
	mu   sync.Mutex
	buf  []sdktrace.ReadOnlySpan
}

func (e *fileExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range spans {
		e.buf = append(e.buf, s)
		if p := s.Parent(); !p.IsValid() || p.IsRemote() {
			e.flush()
		}
	}
	return nil
}

func (e *fileExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.flush()
	return nil
}

// flush writes the buffer as one line; errors drop the spans (never fail f).
func (e *fileExporter) flush() {
	if len(e.buf) == 0 {
		return
	}
	line, err := encodeLine(request(e.buf))
	e.buf = nil
	if err == nil {
		_ = appendLine(e.path, line)
	}
}

// appendLine appends line in one O_APPEND write, so concurrent f processes
// don't interleave.
func appendLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}
