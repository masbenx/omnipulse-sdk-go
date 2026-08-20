package omnipulse

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"
)

// Config holds the SDK configuration
type Config struct {
	// APIUrl is the OmniPulse backend URL (required)
	APIUrl string
	// IngestKey is the X-Ingest-Key for authentication (required)
	IngestKey string
	// Environment is the deployment environment (default: production)
	Environment string
	// ServiceName is the name of your service
	ServiceName string
	// Version is your application version
	Version string
	// Debug enables debug logging
	Debug bool
	// BatchSize is the number of items to batch before sending (default: 100)
	BatchSize int
	// FlushInterval is how often to flush the buffer (default: 5s)
	FlushInterval time.Duration
	// Timeout for HTTP requests (default: 5s)
	Timeout time.Duration
	// HTTPClient overrides the default HTTP client. Intended for tests and custom transports.
	HTTPClient *http.Client
	// EnableProfiling enables continuous CPU profiling (default: false)
	EnableProfiling bool
}

// Client is the main OmniPulse SDK client
type Client struct {
	config     Config
	httpClient *http.Client
	logger     *Logger
	tracer     *Tracer
	metrics    *Metrics

	logBuffer      []LogEntry
	spanBuffer     []SpanData
	metricBuffer   []MetricData
	jobBuffer      []JobData
	requestBuffer  []RequestData
	errorBuffer    []ErrorData
	outgoingBuffer []OutgoingData
	queryBuffer    []QueryData
	cacheBuffer    []CacheData
	bufferMu       sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a new OmniPulse client
func New(cfg Config) (*Client, error) {
	if cfg.APIUrl == "" {
		cfg.APIUrl = os.Getenv("OMNIPULSE_URL")
	}
	if cfg.IngestKey == "" {
		cfg.IngestKey = os.Getenv("OMNIPULSE_INGEST_KEY")
	}
	if cfg.APIUrl == "" || cfg.IngestKey == "" {
		return nil, fmt.Errorf("APIUrl and IngestKey are required")
	}

	// Set defaults
	if cfg.Environment == "" {
		cfg.Environment = "production"
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: cfg.Timeout,
		}
	}

	c := &Client{
		config:         cfg,
		httpClient:     httpClient,
		ctx:            ctx,
		cancel:         cancel,
		logBuffer:      make([]LogEntry, 0, cfg.BatchSize),
		spanBuffer:     make([]SpanData, 0, cfg.BatchSize),
		metricBuffer:   make([]MetricData, 0, cfg.BatchSize),
		jobBuffer:      make([]JobData, 0, cfg.BatchSize),
		requestBuffer:  make([]RequestData, 0, cfg.BatchSize),
		errorBuffer:    make([]ErrorData, 0, cfg.BatchSize),
		outgoingBuffer: make([]OutgoingData, 0, cfg.BatchSize),
		queryBuffer:    make([]QueryData, 0, cfg.BatchSize),
		cacheBuffer:    make([]CacheData, 0, cfg.BatchSize),
	}

	c.logger = newLogger(c)
	c.tracer = newTracer(c)
	c.metrics = newMetrics(c)

	// Start background flush worker
	c.wg.Add(1)
	go c.flushWorker()

	if c.config.EnableProfiling {
		c.wg.Add(1)
		go c.startProfiler()
	}

	return c, nil
}

// Logger returns the logger instance
func (c *Client) Logger() *Logger {
	return c.logger
}

// Tracer returns the tracer instance
func (c *Client) Tracer() *Tracer {
	return c.tracer
}

// Metrics returns the metrics instance
func (c *Client) Metrics() *Metrics {
	return c.metrics
}

// Test sends a test log entry to verify connectivity
func (c *Client) Test() error {
	c.logger.Info("OmniPulse SDK test message", map[string]interface{}{
		"sdk_version": Version,
		"go_version":  runtime.Version(),
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
	})

	// Force immediate flush
	return c.Flush()
}

// Flush immediately sends all buffered data
func (c *Client) Flush() error {
	c.bufferMu.Lock()
	logs := c.logBuffer
	spans := c.spanBuffer
	metrics := c.metricBuffer
	jobs := c.jobBuffer
	requests := c.requestBuffer
	errorsData := c.errorBuffer
	outgoings := c.outgoingBuffer
	queries := c.queryBuffer
	caches := c.cacheBuffer

	c.logBuffer = make([]LogEntry, 0, c.config.BatchSize)
	c.spanBuffer = make([]SpanData, 0, c.config.BatchSize)
	c.metricBuffer = make([]MetricData, 0, c.config.BatchSize)
	c.jobBuffer = make([]JobData, 0, c.config.BatchSize)
	c.requestBuffer = make([]RequestData, 0, c.config.BatchSize)
	c.errorBuffer = make([]ErrorData, 0, c.config.BatchSize)
	c.outgoingBuffer = make([]OutgoingData, 0, c.config.BatchSize)
	c.queryBuffer = make([]QueryData, 0, c.config.BatchSize)
	c.cacheBuffer = make([]CacheData, 0, c.config.BatchSize)
	c.bufferMu.Unlock()

	var lastErr error

	if len(logs) > 0 {
		if err := c.sendLogs(logs); err != nil {
			lastErr = err
			if c.config.Debug {
				fmt.Printf("[omnipulse] failed to send logs: %v\n", err)
			}
		}
	}

	if len(spans) > 0 {
		if err := c.sendSpans(spans); err != nil {
			lastErr = err
			if c.config.Debug {
				fmt.Printf("[omnipulse] failed to send spans: %v\n", err)
			}
		}
	}

	if len(metrics) > 0 {
		if err := c.sendMetrics(metrics); err != nil {
			lastErr = err
			if c.config.Debug {
				fmt.Printf("[omnipulse] failed to send metrics: %v\n", err)
			}
		}
	}

	if len(jobs) > 0 {
		for _, job := range jobs {
			if err := c.sendJob(job); err != nil {
				lastErr = err
				if c.config.Debug {
					fmt.Printf("[omnipulse] failed to send job: %v\n", err)
				}
			}
		}
	}

	if len(requests) > 0 {
		for _, req := range requests {
			if err := c.sendRequest(req); err != nil {
				lastErr = err
				if c.config.Debug {
					fmt.Printf("[omnipulse] failed to send request: %v\n", err)
				}
			}
		}
	}

	if len(errorsData) > 0 {
		for _, eD := range errorsData {
			if err := c.sendError(eD); err != nil {
				lastErr = err
				if c.config.Debug {
					fmt.Printf("[omnipulse] failed to send error: %v\n", err)
				}
			}
		}
	}

	if len(outgoings) > 0 {
		for _, oD := range outgoings {
			if err := c.sendOutgoing(oD); err != nil {
				lastErr = err
				if c.config.Debug {
					fmt.Printf("[omnipulse] failed to send outgoing: %v\n", err)
				}
			}
		}
	}

	if len(queries) > 0 {
		for _, qD := range queries {
			if err := c.sendQuery(qD); err != nil {
				lastErr = err
				if c.config.Debug {
					fmt.Printf("[omnipulse] failed to send query: %v\n", err)
				}
			}
		}
	}

	if len(caches) > 0 {
		for _, cD := range caches {
			if err := c.sendCache(cD); err != nil {
				lastErr = err
				if c.config.Debug {
					fmt.Printf("[omnipulse] failed to send cache: %v\n", err)
				}
			}
		}
	}

	return lastErr
}

// Close flushes remaining data and shuts down the client
func (c *Client) Close() error {
	c.cancel()
	c.wg.Wait()
	return c.Flush()
}

func (c *Client) flushWorker() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.config.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = c.Flush()
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) addLog(entry LogEntry) {
	c.bufferMu.Lock()
	c.logBuffer = append(c.logBuffer, entry)
	shouldFlush := len(c.logBuffer) >= c.config.BatchSize
	c.bufferMu.Unlock()

	if shouldFlush {
		go c.Flush()
	}
}

func (c *Client) addError(errData ErrorData) {
	c.bufferMu.Lock()
	c.errorBuffer = append(c.errorBuffer, errData)
	shouldFlush := len(c.errorBuffer) >= c.config.BatchSize
	c.bufferMu.Unlock()

	if shouldFlush {
		go c.Flush()
	}
}

func (c *Client) addSpan(span SpanData) {
	c.bufferMu.Lock()
	c.spanBuffer = append(c.spanBuffer, span)
	shouldFlush := len(c.spanBuffer) >= c.config.BatchSize
	c.bufferMu.Unlock()

	if shouldFlush {
		go c.Flush()
	}
}

func (c *Client) addMetric(metric MetricData) {
	c.bufferMu.Lock()
	c.metricBuffer = append(c.metricBuffer, metric)
	shouldFlush := len(c.metricBuffer) >= c.config.BatchSize
	c.bufferMu.Unlock()

	if shouldFlush {
		go c.Flush()
	}
}

func (c *Client) sendLogs(logs []LogEntry) error {
	payload := map[string]interface{}{
		"entries": logs,
	}
	return c.send("/api/ingest/app-logs", payload)
}

func (c *Client) sendSpans(spans []SpanData) error {
	payload := map[string]interface{}{
		"spans": spans,
	}
	return c.send("/api/ingest/app-traces", payload)
}

func (c *Client) sendMetrics(metrics []MetricData) error {
	payload := map[string]interface{}{
		"metrics": metrics,
	}
	return c.send("/api/ingest/app-metrics", payload)
}

func (c *Client) sendJob(job JobData) error {
	return c.send("/api/ingest/app-job", job)
}

func (c *Client) sendRequest(req RequestData) error {
	return c.send("/api/ingest/app-request", req)
}

func (c *Client) sendError(errData ErrorData) error {
	return c.send("/api/ingest/app-errors", errData)
}

func (c *Client) sendOutgoing(data OutgoingData) error {
	return c.send("/api/ingest/app-outgoing", data)
}

func (c *Client) sendQuery(data QueryData) error {
	return c.send("/api/ingest/app-query", data)
}

func (c *Client) sendCache(data CacheData) error {
	return c.send("/api/ingest/app-cache", data)
}

func (c *Client) LogJob(job JobData) {
	if job.Ts == "" {
		job.Ts = time.Now().UTC().Format(time.RFC3339)
	}

	c.bufferMu.Lock()
	c.jobBuffer = append(c.jobBuffer, job)
	shouldFlush := len(c.jobBuffer) >= c.config.BatchSize
	c.bufferMu.Unlock()

	if shouldFlush {
		go c.Flush()
	}
}

type JobData struct {
	JobName    string `json:"job_name"`
	Queue      string `json:"queue"`
	DurationMs int    `json:"duration_ms"`
	WaitTimeMs int    `json:"wait_time_ms"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	Ts         string `json:"ts"`
}

type ErrorData struct {
	Type      string                 `json:"type"`
	Message   string                 `json:"message"`
	Stack     string                 `json:"stack,omitempty"`
	Timestamp string                 `json:"timestamp"`
	Service   string                 `json:"service,omitempty"`
	Meta      map[string]interface{} `json:"meta,omitempty"`
}

type RequestData struct {
	Timestamp  string `json:"timestamp"`
	Method     string `json:"method"`
	Route      string `json:"route"`
	Status     int    `json:"status"`
	DurationMs int    `json:"duration_ms"`
	Env        string `json:"env,omitempty"`
	TraceID    string `json:"trace_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

type OutgoingData struct {
	Timestamp  string `json:"timestamp"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	Status     int    `json:"status"`
	DurationMs int    `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	Env        string `json:"env,omitempty"`
	TraceID    string `json:"trace_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

type QueryData struct {
	Timestamp   string `json:"timestamp"`
	DBSystem    string `json:"db_system"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Statement   string `json:"statement"`
	DurationMs  int    `json:"duration_ms"`
	Rows        int    `json:"rows,omitempty"`
	Error       string `json:"error,omitempty"`
	Route       string `json:"route,omitempty"`
	Env         string `json:"env,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
}

type CacheData struct {
	Timestamp  string `json:"timestamp"`
	Operation  string `json:"operation"`
	Key        string `json:"key"`
	Hit        *bool  `json:"hit,omitempty"`
	DurationMs int    `json:"duration_ms"`
	Store      string `json:"store"`
	Env        string `json:"env,omitempty"`
	TraceID    string `json:"trace_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

func (c *Client) LogRequest(req RequestData) {
	if req.Timestamp == "" {
		req.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if req.Env == "" {
		req.Env = c.config.Environment
	}

	c.bufferMu.Lock()
	c.requestBuffer = append(c.requestBuffer, req)
	shouldFlush := len(c.requestBuffer) >= c.config.BatchSize
	c.bufferMu.Unlock()

	if shouldFlush {
		go c.Flush()
	}
}

func (c *Client) send(endpoint string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Compress with gzip
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		return fmt.Errorf("failed to compress payload: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}

	req, err := http.NewRequestWithContext(c.ctx, "POST", c.config.APIUrl+endpoint, &buf)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-Ingest-Key", c.config.IngestKey)
	req.Header.Set("User-Agent", fmt.Sprintf("omnipulse-go-sdk/%s", Version))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	return nil
}
func (c *Client) CaptureError(err error, meta map[string]interface{}) {
	if err == nil {
		return
	}

	errData := ErrorData{
		Type:      "Error",
		Message:   err.Error(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Service:   c.config.ServiceName,
		Meta:      meta,
	}

	c.addError(errData)
}

func (c *Client) CaptureOutgoing(data OutgoingData) {
	if data.Timestamp == "" {
		data.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	c.bufferMu.Lock()
	c.outgoingBuffer = append(c.outgoingBuffer, data)
	shouldFlush := len(c.outgoingBuffer) >= c.config.BatchSize
	c.bufferMu.Unlock()

	if shouldFlush {
		go c.Flush()
	}
}

func (c *Client) CaptureQuery(data QueryData) {
	if data.Timestamp == "" {
		data.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	c.bufferMu.Lock()
	c.queryBuffer = append(c.queryBuffer, data)
	shouldFlush := len(c.queryBuffer) >= c.config.BatchSize
	c.bufferMu.Unlock()

	if shouldFlush {
		go c.Flush()
	}
}

func (c *Client) CaptureCache(data CacheData) {
	if data.Timestamp == "" {
		data.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	c.bufferMu.Lock()
	c.cacheBuffer = append(c.cacheBuffer, data)
	shouldFlush := len(c.cacheBuffer) >= c.config.BatchSize
	c.bufferMu.Unlock()

	if shouldFlush {
		go c.Flush()
	}
}

const Version = "1.1.0"
