package omnipulse

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// --- Client Init Tests ---

func TestNewClient_RequiresAPIUrlAndIngestKey(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected error when APIUrl and IngestKey are empty")
	}
}

func TestNewClient_RequiresIngestKey(t *testing.T) {
	_, err := New(Config{APIUrl: "http://localhost"})
	if err == nil {
		t.Fatal("expected error when IngestKey is empty")
	}
}

func TestNewClient_RequiresAPIUrl(t *testing.T) {
	_, err := New(Config{IngestKey: "test-key"})
	if err == nil {
		t.Fatal("expected error when APIUrl is empty")
	}
}

func TestNewClient_Success(t *testing.T) {
	c, err := New(Config{APIUrl: "http://localhost", IngestKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer c.Close()

	if c.Logger() == nil {
		t.Fatal("logger should not be nil")
	}
	if c.Tracer() == nil {
		t.Fatal("tracer should not be nil")
	}
	if c.Metrics() == nil {
		t.Fatal("metrics should not be nil")
	}
}

func TestNewClient_Defaults(t *testing.T) {
	c, err := New(Config{APIUrl: "http://localhost", IngestKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer c.Close()

	if c.config.Environment != "production" {
		t.Errorf("expected default environment 'production', got %q", c.config.Environment)
	}
	if c.config.BatchSize != 100 {
		t.Errorf("expected default BatchSize 100, got %d", c.config.BatchSize)
	}
	if c.config.FlushInterval != 5*time.Second {
		t.Errorf("expected default FlushInterval 5s, got %v", c.config.FlushInterval)
	}
	if c.config.Timeout != 5*time.Second {
		t.Errorf("expected default Timeout 5s, got %v", c.config.Timeout)
	}
}

func TestNewClient_CustomConfig(t *testing.T) {
	c, err := New(Config{
		APIUrl:        "http://localhost",
		IngestKey:     "test-key",
		Environment:   "staging",
		ServiceName:   "my-service",
		Version:       "1.0.0",
		BatchSize:     50,
		FlushInterval: 10 * time.Second,
		Timeout:       3 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer c.Close()

	if c.config.Environment != "staging" {
		t.Errorf("expected environment 'staging', got %q", c.config.Environment)
	}
	if c.config.BatchSize != 50 {
		t.Errorf("expected BatchSize 50, got %d", c.config.BatchSize)
	}
	if c.config.ServiceName != "my-service" {
		t.Errorf("expected ServiceName 'my-service', got %q", c.config.ServiceName)
	}
}

// --- Logger Tests ---

func TestLogger_AllLevels(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key", ServiceName: "test-svc"})
	defer c.Close()

	levels := []struct {
		name string
		fn   func(string, ...map[string]interface{})
		want LogLevel
	}{
		{"Debug", c.Logger().Debug, LogLevelDebug},
		{"Info", c.Logger().Info, LogLevelInfo},
		{"Warn", c.Logger().Warn, LogLevelWarn},
		{"Error", c.Logger().Error, LogLevelError},
		{"Fatal", c.Logger().Fatal, LogLevelFatal},
	}

	for _, tt := range levels {
		t.Run(tt.name, func(t *testing.T) {
			c.bufferMu.Lock()
			c.logBuffer = c.logBuffer[:0]
			c.bufferMu.Unlock()

			tt.fn("test message", map[string]interface{}{"key": "value"})

			c.bufferMu.Lock()
			if len(c.logBuffer) != 1 {
				t.Fatalf("expected 1 log entry, got %d", len(c.logBuffer))
			}
			entry := c.logBuffer[0]
			c.bufferMu.Unlock()

			if entry.Level != tt.want {
				t.Errorf("expected level %q, got %q", tt.want, entry.Level)
			}
			if entry.Message != "test message" {
				t.Errorf("expected message 'test message', got %q", entry.Message)
			}
			if entry.ServiceName != "test-svc" {
				t.Errorf("expected service name 'test-svc', got %q", entry.ServiceName)
			}
			if entry.Tags["key"] != "value" {
				t.Errorf("expected tag key=value, got %v", entry.Tags)
			}
		})
	}
}

func TestLogger_WithContext(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	span := c.Tracer().StartSpan("test-span")

	c.Logger().WithContext(span, LogLevelInfo, "contextualized", map[string]interface{}{"ctx": true})

	c.bufferMu.Lock()
	if len(c.logBuffer) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(c.logBuffer))
	}
	entry := c.logBuffer[0]
	c.bufferMu.Unlock()

	if entry.TraceID != span.TraceID {
		t.Errorf("expected trace ID %q, got %q", span.TraceID, entry.TraceID)
	}
	if entry.SpanID != span.SpanID {
		t.Errorf("expected span ID %q, got %q", span.SpanID, entry.SpanID)
	}
}

func TestMergeTags_Empty(t *testing.T) {
	result := mergeTags(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestMergeTags_Multiple(t *testing.T) {
	result := mergeTags([]map[string]interface{}{
		{"a": 1},
		{"b": 2},
	})
	if result["a"] != 1 || result["b"] != 2 {
		t.Errorf("expected merged tags, got %v", result)
	}
}

// --- Tracer Tests ---

func TestTracer_StartSpan(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key", ServiceName: "test"})
	defer c.Close()

	span := c.Tracer().StartSpan("test-operation")

	if span.TraceID == "" {
		t.Fatal("TraceID should not be empty")
	}
	if span.SpanID == "" {
		t.Fatal("SpanID should not be empty")
	}
	if span.Name != "test-operation" {
		t.Errorf("expected name 'test-operation', got %q", span.Name)
	}
	if span.Status != SpanStatusOK {
		t.Errorf("expected status OK, got %q", span.Status)
	}
	if span.StartTime.IsZero() {
		t.Fatal("StartTime should not be zero")
	}
}

func TestTracer_SpanWithParent(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	parent := c.Tracer().StartSpan("parent")
	child := c.Tracer().StartSpan("child", WithParent(parent))

	if child.TraceID != parent.TraceID {
		t.Errorf("child should inherit parent's TraceID: %q != %q", child.TraceID, parent.TraceID)
	}
	if child.ParentSpanID != parent.SpanID {
		t.Errorf("child's ParentSpanID should be parent's SpanID: %q != %q", child.ParentSpanID, parent.SpanID)
	}
}

func TestTracer_SpanWithOptions(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	span := c.Tracer().StartSpan("test",
		WithTraceID("custom-trace-id"),
		WithParentSpanID("custom-parent"),
		WithAttributes(map[string]interface{}{"key": "value"}),
	)

	if span.TraceID != "custom-trace-id" {
		t.Errorf("expected TraceID 'custom-trace-id', got %q", span.TraceID)
	}
	if span.ParentSpanID != "custom-parent" {
		t.Errorf("expected ParentSpanID 'custom-parent', got %q", span.ParentSpanID)
	}
	if span.Attributes["key"] != "value" {
		t.Errorf("expected attribute key=value, got %v", span.Attributes)
	}
}

func TestSpan_SetAttribute(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	span := c.Tracer().StartSpan("test")
	span.SetAttribute("http.status_code", 200)

	if span.Attributes["http.status_code"] != 200 {
		t.Errorf("expected attribute http.status_code=200, got %v", span.Attributes["http.status_code"])
	}
}

func TestSpan_SetStatus(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	span := c.Tracer().StartSpan("test")
	span.SetStatus(SpanStatusError, "something failed")

	if span.Status != SpanStatusError {
		t.Errorf("expected status error, got %q", span.Status)
	}
	if span.StatusMsg != "something failed" {
		t.Errorf("expected status message, got %q", span.StatusMsg)
	}
}

func TestSpan_AddEvent(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	span := c.Tracer().StartSpan("test")
	span.AddEvent("cache.miss", map[string]interface{}{"key": "user:123"})

	if len(span.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(span.Events))
	}
	if span.Events[0].Name != "cache.miss" {
		t.Errorf("expected event name 'cache.miss', got %q", span.Events[0].Name)
	}
}

func TestSpan_End(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	span := c.Tracer().StartSpan("test")
	time.Sleep(1 * time.Millisecond) // ensure measurable duration
	span.End()

	c.bufferMu.Lock()
	if len(c.spanBuffer) != 1 {
		t.Fatalf("expected 1 span in buffer, got %d", len(c.spanBuffer))
	}
	data := c.spanBuffer[0]
	c.bufferMu.Unlock()

	if data.DurationNs <= 0 {
		t.Errorf("expected positive duration, got %d", data.DurationNs)
	}
}

func TestContextWithSpan(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	span := c.Tracer().StartSpan("test")
	ctx := ContextWithSpan(c.ctx, span)

	retrieved := SpanFromContext(ctx)
	if retrieved == nil {
		t.Fatal("expected span in context")
	}
	if retrieved.SpanID != span.SpanID {
		t.Errorf("expected span ID %q, got %q", span.SpanID, retrieved.SpanID)
	}
}

func TestSpanFromContext_Nil(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	retrieved := SpanFromContext(c.ctx)
	if retrieved != nil {
		t.Error("expected nil span from empty context")
	}
}

// --- Metrics Tests ---

func TestMetrics_Counter(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key", ServiceName: "test"})
	defer c.Close()

	c.Metrics().Counter("requests.total", 1, map[string]string{"method": "GET"})

	c.bufferMu.Lock()
	if len(c.metricBuffer) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(c.metricBuffer))
	}
	m := c.metricBuffer[0]
	c.bufferMu.Unlock()

	if m.Name != "requests.total" {
		t.Errorf("expected name 'requests.total', got %q", m.Name)
	}
	if m.Type != MetricTypeCounter {
		t.Errorf("expected type counter, got %q", m.Type)
	}
	if m.Value != 1 {
		t.Errorf("expected value 1, got %f", m.Value)
	}
	if m.Tags["method"] != "GET" {
		t.Errorf("expected tag method=GET, got %v", m.Tags)
	}
	if m.ServiceName != "test" {
		t.Errorf("expected service name 'test', got %q", m.ServiceName)
	}
}

func TestMetrics_Gauge(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	c.Metrics().Gauge("cpu.usage", 75.5)

	c.bufferMu.Lock()
	m := c.metricBuffer[0]
	c.bufferMu.Unlock()

	if m.Type != MetricTypeGauge {
		t.Errorf("expected gauge type, got %q", m.Type)
	}
	if m.Value != 75.5 {
		t.Errorf("expected value 75.5, got %f", m.Value)
	}
}

func TestMetrics_Histogram(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	c.Metrics().Histogram("response.time", 123.45)

	c.bufferMu.Lock()
	m := c.metricBuffer[0]
	c.bufferMu.Unlock()

	if m.Type != MetricTypeHistogram {
		t.Errorf("expected histogram type, got %q", m.Type)
	}
}

func TestMetrics_RecordDuration(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	c.Metrics().RecordDuration("http.duration", 250*time.Millisecond)

	c.bufferMu.Lock()
	m := c.metricBuffer[0]
	c.bufferMu.Unlock()

	if m.Value != 250 {
		t.Errorf("expected value 250ms, got %f", m.Value)
	}
}

func TestMetrics_IncrementDecrement(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	c.Metrics().Increment("counter")
	c.Metrics().Decrement("counter")

	c.bufferMu.Lock()
	if len(c.metricBuffer) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(c.metricBuffer))
	}
	if c.metricBuffer[0].Value != 1 {
		t.Errorf("increment should be 1, got %f", c.metricBuffer[0].Value)
	}
	if c.metricBuffer[1].Value != -1 {
		t.Errorf("decrement should be -1, got %f", c.metricBuffer[1].Value)
	}
	c.bufferMu.Unlock()
}

// --- Flush & Send Tests ---

func TestFlush_SendsLogs(t *testing.T) {
	var received int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("X-Ingest-Key") != "test-key" {
			t.Errorf("expected X-Ingest-Key 'test-key', got %q", r.Header.Get("X-Ingest-Key"))
		}
		if r.Header.Get("Content-Encoding") != "gzip" {
			t.Errorf("expected gzip encoding")
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("expected User-Agent header")
		}

		// Decompress and parse
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatalf("gzip error: %v", err)
		}
		body, _ := io.ReadAll(gz)
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("json error: %v", err)
		}

		atomic.AddInt32(&received, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c, _ := New(Config{APIUrl: srv.URL, IngestKey: "test-key"})
	c.Logger().Info("test log message")

	err := c.Flush()
	if err != nil {
		t.Fatalf("flush failed: %v", err)
	}

	if atomic.LoadInt32(&received) != 1 {
		t.Errorf("expected 1 request, got %d", atomic.LoadInt32(&received))
	}
	c.Close()
}

func TestFlush_SendsSpans(t *testing.T) {
	var received int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ingest/traces" {
			return
		}
		atomic.AddInt32(&received, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c, _ := New(Config{APIUrl: srv.URL, IngestKey: "key"})
	span := c.Tracer().StartSpan("test")
	span.End()

	c.Flush()

	if atomic.LoadInt32(&received) != 1 {
		t.Errorf("expected 1 trace request, got %d", atomic.LoadInt32(&received))
	}
	c.Close()
}

func TestFlush_SendsMetrics(t *testing.T) {
	var received int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ingest/app-metrics" {
			return
		}
		atomic.AddInt32(&received, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c, _ := New(Config{APIUrl: srv.URL, IngestKey: "key"})
	c.Metrics().Counter("test", 1)

	c.Flush()

	if atomic.LoadInt32(&received) != 1 {
		t.Errorf("expected 1 metrics request, got %d", atomic.LoadInt32(&received))
	}
	c.Close()
}

func TestFlush_EmptyBuffers(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	// Flush with no data should succeed without errors
	err := c.Flush()
	if err != nil {
		t.Fatalf("empty flush should not error: %v", err)
	}
}

func TestSend_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c, _ := New(Config{APIUrl: srv.URL, IngestKey: "key"})
	c.Logger().Info("test")

	err := c.Flush()
	if err == nil {
		t.Fatal("expected error on server 500")
	}
	c.Close()
}

func TestSend_VerifiesEndpoints(t *testing.T) {
	endpoints := make(map[string]bool)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoints[r.URL.Path] = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c, _ := New(Config{APIUrl: srv.URL, IngestKey: "key"})

	// Add all types
	c.Logger().Info("log")
	c.Tracer().StartSpan("span").End()
	c.Metrics().Counter("metric", 1)
	c.LogJob(JobData{JobName: "job", Status: "ok"})

	c.Flush()

	expected := []string{"/api/ingest/logs", "/api/ingest/traces", "/api/ingest/app-metrics", "/api/ingest/app-job"}
	for _, ep := range expected {
		if !endpoints[ep] {
			t.Errorf("expected endpoint %q to be called", ep)
		}
	}
	c.Close()
}

// --- Close/Lifecycle Tests ---

func TestClose_FlushesRemaining(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost:1", IngestKey: "key"})

	c.Logger().Info("will be flushed on close")

	// Verify buffer has data before close
	c.bufferMu.Lock()
	beforeClose := len(c.logBuffer)
	c.bufferMu.Unlock()

	if beforeClose != 1 {
		t.Fatalf("expected 1 log in buffer before close, got %d", beforeClose)
	}

	c.Close() // Should attempt flush (may fail due to unreachable server, but buffer should be drained)

	// Buffer should be drained after close
	c.bufferMu.Lock()
	afterClose := len(c.logBuffer)
	c.bufferMu.Unlock()

	if afterClose != 0 {
		t.Errorf("expected empty buffer after close, got %d", afterClose)
	}
}

// --- HTTP Middleware Tests ---

func TestHTTPMiddleware_SkipsHealthEndpoints(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	handler := HTTPMiddleware(c)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	for _, path := range []string{"/health", "/healthz", "/ready", "/readyz"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Errorf("expected 200 for %s, got %d", path, rec.Code)
		}
	}

	// No spans should be created for health endpoints
	c.bufferMu.Lock()
	spanCount := len(c.spanBuffer)
	c.bufferMu.Unlock()

	if spanCount != 0 {
		t.Errorf("expected 0 spans for health endpoints, got %d", spanCount)
	}
}

func TestHTTPMiddleware_CreatesSpan(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key", ServiceName: "test"})
	defer c.Close()

	handler := HTTPMiddleware(c)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify span is available in context
		span := GetSpanFromContext(r)
		if span == nil {
			t.Error("expected span in request context")
		}

		traceID := GetTraceIDFromContext(r)
		if traceID == "" {
			t.Error("expected trace ID in request context")
		}

		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Check trace header in response
	if rec.Header().Get("X-OmniPulse-Trace-ID") == "" {
		t.Error("expected X-OmniPulse-Trace-ID header in response")
	}

	// Span should be in buffer
	c.bufferMu.Lock()
	if len(c.spanBuffer) != 1 {
		t.Fatalf("expected 1 span, got %d", len(c.spanBuffer))
	}
	span := c.spanBuffer[0]
	c.bufferMu.Unlock()

	if span.Name != "GET /api/test" {
		t.Errorf("expected span name 'GET /api/test', got %q", span.Name)
	}
}

func TestHTTPMiddleware_TracesPropagation(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	handler := HTTPMiddleware(c)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-OmniPulse-Trace-ID", "custom-trace-123")
	req.Header.Set("X-OmniPulse-Span-ID", "parent-span-456")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	c.bufferMu.Lock()
	span := c.spanBuffer[0]
	c.bufferMu.Unlock()

	if span.TraceID != "custom-trace-123" {
		t.Errorf("expected propagated TraceID, got %q", span.TraceID)
	}
	if span.ParentSpanID != "parent-span-456" {
		t.Errorf("expected propagated ParentSpanID, got %q", span.ParentSpanID)
	}
}

// --- LogJob Tests ---

func TestLogJob(t *testing.T) {
	c, _ := New(Config{APIUrl: "http://localhost", IngestKey: "key"})
	defer c.Close()

	c.LogJob(JobData{
		JobName:    "email.send",
		Queue:      "default",
		DurationMs: 250,
		Status:     "success",
	})

	c.bufferMu.Lock()
	if len(c.jobBuffer) != 1 {
		t.Fatalf("expected 1 job, got %d", len(c.jobBuffer))
	}
	job := c.jobBuffer[0]
	c.bufferMu.Unlock()

	if job.JobName != "email.send" {
		t.Errorf("expected job name 'email.send', got %q", job.JobName)
	}
	if job.Ts == "" {
		t.Error("expected Ts to be auto-filled")
	}
}

// --- Generate ID Tests ---

func TestGenerateID(t *testing.T) {
	id1 := generateID(16)
	id2 := generateID(16)

	if len(id1) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("expected 32 char hex, got %d chars", len(id1))
	}
	if id1 == id2 {
		t.Error("expected unique IDs")
	}

	id3 := generateID(8)
	if len(id3) != 16 { // 8 bytes = 16 hex chars
		t.Errorf("expected 16 char hex, got %d chars", len(id3))
	}
}

// --- Version Tests ---

func TestVersion(t *testing.T) {
	if Version == "" {
		t.Error("Version should not be empty")
	}
}
