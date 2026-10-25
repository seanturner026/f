// Package tracing records f's spans as OTLP/JSON lines in ~/.cache/f/traces.jsonl
// (one line per root span), for `f trace` (Jaeger) and `f trace stats`.
// Tracing never fails a command: if the file can't be written, spans are dropped.
package tracing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const scopeName = "github.com/seanturner026/pi/f"

var (
	tracer   trace.Tracer = noop.NewTracerProvider().Tracer(scopeName)
	shutdown              = func() {}
	once     sync.Once
)

// Path is the traces file: $F_TRACE_FILE, else ~/.cache/f/traces.jsonl.
func Path() string {
	if p := os.Getenv("F_TRACE_FILE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "f", "traces.jsonl")
}

// Init turns tracing on for this process. Call Shutdown before exiting.
func Init() {
	exp := &fileExporter{path: Path()}
	res := resource.NewSchemaless(semconv.ServiceName("f"), semconv.ProcessPID(os.Getpid()))
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp), sdktrace.WithResource(res))
	tracer = tp.Tracer(scopeName)
	shutdown = func() { _ = tp.Shutdown(context.Background()) }
}

// Shutdown writes any spans still buffered. Safe to call more than once; call
// it before os.Exit and syscall.Exec, which skip deferred calls.
func Shutdown() { once.Do(func() { shutdown() }) }

// Root starts this process's root span, as a child of $TRACEPARENT when an f
// that started this one passed it (ChildEnv).
func Root(ctx context.Context, name string) (context.Context, trace.Span) {
	ctx = propagation.TraceContext{}.Extract(ctx, envCarrier{})
	caller := os.Getenv("F_CALLER")
	if caller == "" {
		caller = "shell"
	}
	return tracer.Start(ctx, name, trace.WithAttributes(attribute.String("f.caller", caller)))
}

// Finish ends the span in ctx (the root, in practice) and writes everything:
// for paths that leave the process without returning, like syscall.Exec.
func Finish(ctx context.Context) {
	trace.SpanFromContext(ctx).End()
	Shutdown()
}

// Start starts a child span of the span in ctx.
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// End ends span, marking it failed when err is set.
func End(span trace.Span, err error) {
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// Rename names the current span once the command knows what it is (f's root
// action is the picker, f open or f start).
func Rename(ctx context.Context, name string) { trace.SpanFromContext(ctx).SetName(name) }

// ChildEnv is the environment for an f started detached: this one's, with the
// span in ctx as TRACEPARENT and F_CALLER=f.
func ChildEnv(ctx context.Context) []string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	env := []string{"F_CALLER=f"}
	for k, v := range carrier {
		env = append(env, strings.ToUpper(k)+"="+v)
	}
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); k != "F_CALLER" && k != "TRACEPARENT" && k != "TRACESTATE" {
			env = append(env, kv)
		}
	}
	return env
}

// envCarrier reads W3C trace context from the environment (TRACEPARENT).
type envCarrier struct{}

func (envCarrier) Get(key string) string { return os.Getenv(strings.ToUpper(key)) }
func (envCarrier) Set(string, string)    {}
func (envCarrier) Keys() []string        { return []string{"traceparent", "tracestate"} }

// Runner is what packages that shell out call (shell.Exec implements it).
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// Traced records each command Runner runs as a span named after the command
// and subcommand ("git rev-list", "gh pr view"), with its dir relative to Base.
type Traced struct {
	Runner
	Base string
}

func (t Traced) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd, cmdArgs := unwrapEnv(name, args)
	attrs := []attribute.KeyValue{attribute.String("exec.dir", t.rel(dir))}
	if cmd != "bd" { // bd notes and descriptions hold arbitrary text
		attrs = append(attrs, attribute.String("exec.args", truncate(strings.Join(cmdArgs, " "), 60)))
	}
	ctx, span := Start(ctx, CommandName(cmd, cmdArgs), attrs...)
	out, err := t.Runner.Run(ctx, dir, name, args...)
	code := 0
	if err != nil {
		code = -1
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		// The error text holds the full args (see above): status says only how it ended.
		span.SetStatus(codes.Error, "exit "+strconv.Itoa(code))
	}
	span.SetAttributes(attribute.Int("exec.exit_code", code))
	span.End()
	return out, err
}

func (t Traced) rel(dir string) string {
	if dir == "" {
		return "."
	}
	if r, err := filepath.Rel(t.Base, dir); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return dir
}

// unwrapEnv sees through `env VAR=value... cmd args` (git snapshots use a
// temp GIT_INDEX_FILE), so the span is named after cmd, not a per-run path.
func unwrapEnv(name string, args []string) (string, []string) {
	if name != "env" {
		return name, args
	}
	for i, a := range args {
		if !strings.Contains(a, "=") && !strings.HasPrefix(a, "-") {
			return a, args[i+1:]
		}
	}
	return name, args
}

// CommandName is a command's span name: the command and its first
// subcommand, two for gh and herdr ("gh pr view", "herdr tab create").
func CommandName(name string, args []string) string {
	words := 1
	if name == "gh" || name == "herdr" {
		words = 2
	}
	parts := []string{name}
	for _, a := range args {
		if len(parts) > words || strings.HasPrefix(a, "-") {
			break
		}
		parts = append(parts, a)
		if a == "api" { // gh api <path>: the path is an argument, not a subcommand
			break
		}
	}
	return strings.Join(parts, " ")
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
