package tracing

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// useTracer points the package at tp for one test.
func useTracer(t *testing.T, tp *sdktrace.TracerProvider) {
	t.Helper()
	old := tracer
	tracer = tp.Tracer(scopeName)
	t.Cleanup(func() { tracer = old })
}

func TestExporterWritesOTLPLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traces.jsonl")
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(&fileExporter{path: path}))
	useTracer(t, tp)

	ctx, root := Root(context.Background(), "f sync")
	_, child := Start(ctx, "git fetch")
	child.End()
	root.End()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (one per root span)", len(lines))
	}
	if !strings.Contains(lines[0], `"traceId":"`+root.SpanContext().TraceID().String()+`"`) {
		t.Errorf("line doesn't carry the hex trace ID: %s", lines[0])
	}
	req, err := decodeLine([]byte(lines[0]))
	if err != nil {
		t.Fatalf("line isn't OTLP: %v", err)
	}
	spans := spansOf(req)
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	byName := map[string]*tracepb.Span{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	r, c := byName["f sync"], byName["git fetch"]
	if r == nil || c == nil {
		t.Fatalf("spans: %v", byName)
	}
	if !bytes.Equal(c.ParentSpanId, r.SpanId) || !bytes.Equal(c.TraceId, r.TraceId) {
		t.Errorf("child parent=%x trace=%x, want parent=%x trace=%x", c.ParentSpanId, c.TraceId, r.SpanId, r.TraceId)
	}
	if len(r.ParentSpanId) != 0 {
		t.Errorf("root has a parent: %x", r.ParentSpanId)
	}
}

type fakeRunner struct{ err error }

func (f fakeRunner) Run(context.Context, string, string, ...string) (string, error) {
	return "ok", f.err
}

func TestTracedRunnerRecordsSpan(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	useTracer(t, sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	failing := Traced{Runner: fakeRunner{err: fmt.Errorf("git rev-list: boom: %w", &exec.ExitError{})}, Base: "/base"}
	ok := Traced{Runner: fakeRunner{}, Base: "/base"}

	_, _ = failing.Run(context.Background(), "/base/.features/x/gateway", "git", "rev-list", "--count", "HEAD")
	_, _ = ok.Run(context.Background(), "/base", "bd", "note", "f-1", "secret text")

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	git, bd := spans[0], spans[1]
	if git.Name != "git rev-list" || git.Status.Code != codes.Error || strings.Contains(git.Status.Description, "boom") {
		t.Errorf("git span: name %q status %v %q", git.Name, git.Status.Code, git.Status.Description)
	}
	attrs := map[string]string{}
	for _, a := range git.Attributes {
		attrs[string(a.Key)] = a.Value.Emit()
	}
	if attrs["exec.dir"] != ".features/x/gateway" || attrs["exec.exit_code"] != "-1" || attrs["exec.args"] != "rev-list --count HEAD" {
		t.Errorf("git attrs: %v", attrs)
	}
	if bd.Name != "bd note" || bd.Status.Code == codes.Error {
		t.Errorf("bd span: %q %v", bd.Name, bd.Status.Code)
	}
	for _, a := range bd.Attributes {
		if a.Key == "exec.args" {
			t.Errorf("bd args recorded: %v", a.Value.Emit())
		}
	}
}

func TestChildEnvPropagatesTrace(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	useTracer(t, sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	ctx, parent := Root(context.Background(), "f start")
	env := ChildEnv(ctx)
	parent.End()

	// The child process: its environment is what ChildEnv built.
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == "TRACEPARENT" || k == "F_CALLER" {
			t.Setenv(k, v)
		}
	}
	_, child := Root(context.Background(), "f setup")
	child.End()

	spans := exp.GetSpans()
	p, c := spans[0], spans[1]
	if c.SpanContext.TraceID() != p.SpanContext.TraceID() {
		t.Errorf("child trace %s, want parent's %s", c.SpanContext.TraceID(), p.SpanContext.TraceID())
	}
	if c.Parent.SpanID() != p.SpanContext.SpanID() || !c.Parent.IsRemote() {
		t.Errorf("child parent %s (remote %v), want %s", c.Parent.SpanID(), c.Parent.IsRemote(), p.SpanContext.SpanID())
	}
	for _, a := range c.Attributes {
		if a.Key == "f.caller" && a.Value.AsString() != "f" {
			t.Errorf("f.caller = %q, want f", a.Value.AsString())
		}
	}
}

func TestTrimDropsOldLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traces.jsonl")
	now := time.Now()
	write := func(name string, end time.Time) {
		line, err := encodeLine(&tracepb.TracesData{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
			TraceId: bytes.Repeat([]byte{1}, 16), SpanId: bytes.Repeat([]byte{2}, 8), Name: name,
			StartTimeUnixNano: uint64(end.Add(-time.Second).UnixNano()), EndTimeUnixNano: uint64(end.UnixNano()),
		}}}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := appendLine(path, line); err != nil {
			t.Fatal(err)
		}
	}
	write("old", now.Add(-8*24*time.Hour))
	write("new", now.Add(-time.Hour))

	if err := Trim(path, now.Add(-Retention)); err != nil {
		t.Fatal(err)
	}
	lines, err := Lines(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(string(lines[0]), `"name":"new"`) {
		t.Errorf("after trim: %q", lines)
	}
}

func TestCommandName(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"git", []string{"rev-list", "--count", "HEAD"}, "git rev-list"},
		{"gh", []string{"pr", "view", "12", "--json", "state"}, "gh pr view"},
		{"gh", []string{"api", "repos/o/r/pulls"}, "gh api"},
		{"herdr", []string{"tab", "create", "--workspace", "w1"}, "herdr tab create"},
		{"bd", []string{"note", "f-1", "text"}, "bd note"},
		{"git", []string{"-c", "x=y", "log"}, "git"},
		{"env", []string{"GIT_INDEX_FILE=/tmp/x123", "git", "write-tree"}, "git write-tree"},
	} {
		if got := CommandName(unwrapEnv(tt.name, tt.args)); got != tt.want {
			t.Errorf("CommandName(%s %v) = %q, want %q", tt.name, tt.args, got, tt.want)
		}
	}
}

func TestStats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traces.jsonl")
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(&fileExporter{path: path}))
	useTracer(t, tp)
	for range 3 {
		ctx, root := Root(context.Background(), "f prs")
		_, child := Start(ctx, "gh pr list")
		child.End()
		root.End()
	}
	var out bytes.Buffer
	if err := Stats(&out, path, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"f prs", "gh pr list", "slowest runs", "(shell)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stats missing %q:\n%s", want, out.String())
		}
	}
}
