package tracing

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// idFields are OTLP's byte IDs: protojson writes them as base64, OTLP/JSON
// (what Jaeger reads) wants hex.
var idFields = map[string]bool{"traceId": true, "spanId": true, "parentSpanId": true}

// request converts finished spans to OTLP TracesData: in JSON the same object
// as an ExportTraceServiceRequest (what Jaeger's /v1/traces takes), without
// pulling the collector package's gRPC dependencies into f.
func request(spans []sdktrace.ReadOnlySpan) *tracepb.TracesData {
	if len(spans) == 0 {
		return &tracepb.TracesData{}
	}
	scope := &tracepb.ScopeSpans{Scope: &commonpb.InstrumentationScope{Name: scopeName}}
	for _, s := range spans {
		sc := s.SpanContext()
		tid, sid := sc.TraceID(), sc.SpanID()
		span := &tracepb.Span{
			TraceId:           tid[:],
			SpanId:            sid[:],
			Name:              s.Name(),
			Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
			StartTimeUnixNano: uint64(s.StartTime().UnixNano()),
			EndTimeUnixNano:   uint64(s.EndTime().UnixNano()),
			Attributes:        keyValues(s.Attributes()),
		}
		if p := s.Parent(); p.IsValid() {
			pid := p.SpanID()
			span.ParentSpanId = pid[:]
		}
		if st := s.Status(); st.Code == codes.Error {
			span.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: st.Description}
		}
		scope.Spans = append(scope.Spans, span)
	}
	return &tracepb.TracesData{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource:   &resourcepb.Resource{Attributes: keyValues(spans[0].Resource().Attributes())},
		ScopeSpans: []*tracepb.ScopeSpans{scope},
	}}}
}

func keyValues(attrs []attribute.KeyValue) []*commonpb.KeyValue {
	out := make([]*commonpb.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		v := &commonpb.AnyValue{}
		switch a.Value.Type() {
		case attribute.BOOL:
			v.Value = &commonpb.AnyValue_BoolValue{BoolValue: a.Value.AsBool()}
		case attribute.INT64:
			v.Value = &commonpb.AnyValue_IntValue{IntValue: a.Value.AsInt64()}
		case attribute.FLOAT64:
			v.Value = &commonpb.AnyValue_DoubleValue{DoubleValue: a.Value.AsFloat64()}
		default:
			v.Value = &commonpb.AnyValue_StringValue{StringValue: a.Value.Emit()}
		}
		out = append(out, &commonpb.KeyValue{Key: string(a.Key), Value: v})
	}
	return out
}

// encodeLine renders req as one OTLP/JSON line (hex IDs), without the newline.
func encodeLine(req *tracepb.TracesData) ([]byte, error) {
	raw, err := protojson.Marshal(req)
	if err != nil {
		return nil, err
	}
	return recodeIDs(raw, func(s string) (string, error) {
		b, err := base64.StdEncoding.DecodeString(s)
		return hex.EncodeToString(b), err
	})
}

// decodeLine parses one OTLP/JSON line (hex IDs) back into a request.
func decodeLine(line []byte) (*tracepb.TracesData, error) {
	raw, err := recodeIDs(line, func(s string) (string, error) {
		b, err := hex.DecodeString(s)
		return base64.StdEncoding.EncodeToString(b), err
	})
	if err != nil {
		return nil, err
	}
	req := &tracepb.TracesData{}
	return req, protojson.Unmarshal(raw, req)
}

// recodeIDs rewrites every ID field in the JSON with conv.
func recodeIDs(raw []byte, conv func(string) (string, error)) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	var walk func(any) error
	walk = func(n any) error {
		switch n := n.(type) {
		case map[string]any:
			for k, child := range n {
				if s, ok := child.(string); ok && idFields[k] {
					c, err := conv(s)
					if err != nil {
						return fmt.Errorf("%s: %w", k, err)
					}
					n[k] = c
				} else if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range n {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
