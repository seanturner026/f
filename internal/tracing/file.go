package tracing

import (
	"bytes"
	"cmp"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"time"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Retention is how long f sync --all keeps traces.
const Retention = 7 * 24 * time.Hour

// Lines returns the file's lines whose newest span ended at or after since.
func Lines(path string, since time.Time) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if req, err := decodeLine(line); err == nil && !lastEnd(req).Before(since) {
			out = append(out, line)
		}
	}
	return out, nil
}

// Trim rewrites the file without lines older than cutoff (or unreadable).
// It renames a temp file over it: an append racing the rewrite may be lost.
func Trim(path string, cutoff time.Time) error {
	if _, err := os.Stat(path); err != nil {
		return nil // nothing traced yet
	}
	keep, err := Lines(path, cutoff)
	if err != nil {
		return err
	}
	tmp := path + ".tmp-" + strconv.Itoa(os.Getpid())
	var buf bytes.Buffer
	for _, l := range keep {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func lastEnd(req *tracepb.TracesData) time.Time {
	var end uint64
	for _, s := range spansOf(req) {
		end = max(end, s.EndTimeUnixNano)
	}
	return time.Unix(0, int64(end))
}

func spansOf(req *tracepb.TracesData) []*tracepb.Span {
	var out []*tracepb.Span
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			out = append(out, ss.Spans...)
		}
	}
	return out
}

// span is a span as stats needs it.
type span struct {
	id, parent, name, caller string
	start                    time.Time
	dur                      time.Duration
}

// Stats writes, for spans that ended since since: count, p50, p95 and max per
// span name, then the slowest root spans with their slowest children.
func Stats(w io.Writer, path string, since time.Time) error {
	lines, err := Lines(path, since)
	if err != nil {
		return err
	}
	var spans []span
	for _, l := range lines {
		req, _ := decodeLine(l)
		for _, s := range spansOf(req) {
			sp := span{
				id:    hex.EncodeToString(s.SpanId),
				name:  s.Name,
				start: time.Unix(0, int64(s.StartTimeUnixNano)),
				dur:   time.Duration(s.EndTimeUnixNano - s.StartTimeUnixNano),
			}
			if len(s.ParentSpanId) > 0 {
				sp.parent = hex.EncodeToString(s.ParentSpanId)
			}
			for _, a := range s.Attributes {
				if a.Key == "f.caller" {
					sp.caller = a.Value.GetStringValue()
				}
			}
			spans = append(spans, sp)
		}
	}
	if len(spans) == 0 {
		_, err := fmt.Fprintf(w, "no spans since %s (%s)\n", since.Format(time.DateTime), path)
		return err
	}

	byName := map[string][]time.Duration{}
	for _, s := range spans {
		byName[s.name] = append(byName[s.name], s.dur)
	}
	names := make([]string, 0, len(byName))
	for n, ds := range byName {
		slices.Sort(ds)
		names = append(names, n)
	}
	// Slowest p95 first: where time goes when it goes badly.
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(pct(byName[b], 95), pct(byName[a], 95)), cmp.Compare(a, b))
	})
	fmt.Fprintf(w, "%d spans since %s\n\n%-34s %6s %8s %8s %8s\n", len(spans), since.Format(time.DateTime), "span", "count", "p50", "p95", "max")
	for _, n := range names {
		ds := byName[n]
		fmt.Fprintf(w, "%-34s %6d %8s %8s %8s\n", n, len(ds), short(pct(ds, 50)), short(pct(ds, 95)), short(ds[len(ds)-1]))
	}

	children := map[string][]span{}
	var roots []span
	for _, s := range spans {
		if s.parent == "" {
			roots = append(roots, s)
		} else {
			children[s.parent] = append(children[s.parent], s)
		}
	}
	slices.SortFunc(roots, func(a, b span) int { return cmp.Compare(b.dur, a.dur) })
	fmt.Fprintf(w, "\nslowest runs\n")
	for _, r := range roots[:min(10, len(roots))] {
		fmt.Fprintf(w, "%8s  %s  %s (%s)\n", short(r.dur), r.name, r.start.Format("01-02 15:04"), r.caller)
		kids := children[r.id]
		slices.SortFunc(kids, func(a, b span) int { return cmp.Compare(b.dur, a.dur) })
		for _, k := range kids[:min(3, len(kids))] {
			fmt.Fprintf(w, "%8s      %s\n", short(k.dur), k.name)
		}
	}
	return nil
}

// pct is the p-th percentile of sorted ds (nearest rank).
func pct(ds []time.Duration, p int) time.Duration {
	i := (len(ds)*p + 99) / 100
	return ds[max(0, min(len(ds)-1, i-1))]
}

func short(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
}
