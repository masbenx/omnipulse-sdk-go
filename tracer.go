package omnipulse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// spanContextKey is the key for storing span in context
type spanContextKey struct{}

// ContextWithSpan returns a new context with the span attached
func ContextWithSpan(ctx context.Context, span *Span) context.Context {
	return context.WithValue(ctx, spanContextKey{}, span)
}

// SpanFromContext retrieves the span from context
func SpanFromContext(ctx context.Context) *Span {
	span, ok := ctx.Value(spanContextKey{}).(*Span)
	if !ok {
		return nil
	}
	return span
}

// SpanStatus represents the status of a span
type SpanStatus string

const (
	SpanStatusOK    SpanStatus = "ok"
	SpanStatusError SpanStatus = "error"
)

// SpanData represents a span for sending to the backend
type SpanData struct {
	TraceID       string                 `json:"trace_id"`
	SpanID        string                 `json:"span_id"`
	ParentSpanID  string                 `json:"parent_span_id,omitempty"`
	Name          string                 `json:"name"`
	ServiceName   string                 `json:"service_name"`
	StartTime     time.Time              `json:"start_time"`
	EndTime       time.Time              `json:"end_time"`
	DurationNs    int64                  `json:"duration_ns"`
	Status        SpanStatus             `json:"status"`
	StatusMessage string                 `json:"status_message,omitempty"`
	Attributes    map[string]interface{} `json:"attributes,omitempty"`
	Events        []SpanEvent            `json:"events,omitempty"`
}

// SpanEvent represents an event within a span
type SpanEvent struct {
	Name       string                 `json:"name"`
	Timestamp  time.Time              `json:"timestamp"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

// Span represents an active span
type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Name         string
	StartTime    time.Time
	Status       SpanStatus
	StatusMsg    string
	Attributes   map[string]interface{}
	Events       []SpanEvent
	tracer       *Tracer
	mu           sync.Mutex
}

// Tracer provides distributed tracing functionality
type Tracer struct {
	client *Client
}

func newTracer(c *Client) *Tracer {
	return &Tracer{client: c}
}

// StartSpan starts a new span
func (t *Tracer) StartSpan(name string, opts ...SpanOption) *Span {
	span := &Span{
		TraceID:    generateID(16),
		SpanID:     generateID(8),
		Name:       name,
		StartTime:  time.Now(),
		Status:     SpanStatusOK,
		Attributes: make(map[string]interface{}),
		tracer:     t,
	}

	for _, opt := range opts {
		opt(span)
	}

	return span
}

// SpanOption is a function that configures a span
type SpanOption func(*Span)

// WithParent sets the parent span
func WithParent(parent *Span) SpanOption {
	return func(s *Span) {
		if parent != nil {
			s.TraceID = parent.TraceID
			s.ParentSpanID = parent.SpanID
		}
	}
}

// WithTraceID sets the trace ID
func WithTraceID(traceID string) SpanOption {
	return func(s *Span) {
		s.TraceID = traceID
	}
}

// WithParentSpanID sets the parent span ID
func WithParentSpanID(parentSpanID string) SpanOption {
	return func(s *Span) {
		s.ParentSpanID = parentSpanID
	}
}

// WithAttributes sets initial attributes
func WithAttributes(attrs map[string]interface{}) SpanOption {
	return func(s *Span) {
		for k, v := range attrs {
			s.Attributes[k] = v
		}
	}
}

// SetAttribute sets an attribute on the span
func (s *Span) SetAttribute(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Attributes[key] = value
}

// SetStatus sets the span status
func (s *Span) SetStatus(status SpanStatus, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
	s.StatusMsg = message
}

// AddEvent adds an event to the span
func (s *Span) AddEvent(name string, attrs ...map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var attributes map[string]interface{}
	if len(attrs) > 0 {
		attributes = attrs[0]
	}

	s.Events = append(s.Events, SpanEvent{
		Name:       name,
		Timestamp:  time.Now(),
		Attributes: attributes,
	})
}

// End ends the span and sends it to the backend
func (s *Span) End() {
	s.mu.Lock()
	endTime := time.Now()
	data := SpanData{
		TraceID:       s.TraceID,
		SpanID:        s.SpanID,
		ParentSpanID:  s.ParentSpanID,
		Name:          s.Name,
		ServiceName:   s.tracer.client.config.ServiceName,
		StartTime:     s.StartTime,
		EndTime:       endTime,
		DurationNs:    endTime.Sub(s.StartTime).Nanoseconds(),
		Status:        s.Status,
		StatusMessage: s.StatusMsg,
		Attributes:    s.Attributes,
		Events:        s.Events,
	}
	s.mu.Unlock()

	s.tracer.client.addSpan(data)
}

// generateID generates a random hex ID
func generateID(bytes int) string {
	b := make([]byte, bytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
