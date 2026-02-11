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
}

// Client is the main OmniPulse SDK client
type Client struct {
	config     Config
	httpClient *http.Client
	logger     *Logger
	tracer     *Tracer
	metrics    *Metrics

	logBuffer    []LogEntry
	spanBuffer   []SpanData
	metricBuffer []MetricData
	jobBuffer    []JobData
	bufferMu     sync.Mutex

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

	c := &Client{
		config: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		ctx:          ctx,
		cancel:       cancel,
		logBuffer:    make([]LogEntry, 0, cfg.BatchSize),
		spanBuffer:   make([]SpanData, 0, cfg.BatchSize),
		metricBuffer: make([]MetricData, 0, cfg.BatchSize),
		jobBuffer:    make([]JobData, 0, cfg.BatchSize),
	}

	c.logger = newLogger(c)
	c.tracer = newTracer(c)
	c.metrics = newMetrics(c)

	// Start background flush worker
	c.wg.Add(1)
	go c.flushWorker()

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
	c.logBuffer = make([]LogEntry, 0, c.config.BatchSize)
	c.spanBuffer = make([]SpanData, 0, c.config.BatchSize)
	c.metricBuffer = make([]MetricData, 0, c.config.BatchSize)
	c.jobBuffer = make([]JobData, 0, c.config.BatchSize)
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
		"logs": logs,
	}
	return c.send("/api/ingest/logs", payload)
}

func (c *Client) sendSpans(spans []SpanData) error {
	payload := map[string]interface{}{
		"spans": spans,
	}
	return c.send("/api/ingest/traces", payload)
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

const Version = "1.1.0"
