package omnipulse

import (
	"time"
)

// LogLevel represents log severity
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
	LogLevelFatal LogLevel = "fatal"
)

// LogEntry represents a single log entry
type LogEntry struct {
	Timestamp   time.Time              `json:"timestamp"`
	Level       LogLevel               `json:"level"`
	Message     string                 `json:"message"`
	ServiceName string                 `json:"service_name,omitempty"`
	TraceID     string                 `json:"trace_id,omitempty"`
	SpanID      string                 `json:"span_id,omitempty"`
	Tags        map[string]interface{} `json:"tags,omitempty"`
	Host        string                 `json:"host,omitempty"`
}

// Logger provides logging functionality
type Logger struct {
	client *Client
}

func newLogger(c *Client) *Logger {
	return &Logger{client: c}
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, tags ...map[string]interface{}) {
	l.log(LogLevelDebug, msg, mergeTags(tags))
}

// Info logs an info message
func (l *Logger) Info(msg string, tags ...map[string]interface{}) {
	l.log(LogLevelInfo, msg, mergeTags(tags))
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, tags ...map[string]interface{}) {
	l.log(LogLevelWarn, msg, mergeTags(tags))
}

// Error logs an error message
func (l *Logger) Error(msg string, tags ...map[string]interface{}) {
	l.log(LogLevelError, msg, mergeTags(tags))
}

// Fatal logs a fatal message
func (l *Logger) Fatal(msg string, tags ...map[string]interface{}) {
	l.log(LogLevelFatal, msg, mergeTags(tags))
}

// WithContext logs a message with trace context from a span
func (l *Logger) WithContext(span *Span, level LogLevel, msg string, tags ...map[string]interface{}) {
	merged := mergeTags(tags)
	if merged == nil {
		merged = make(map[string]interface{})
	}

	entry := LogEntry{
		Timestamp:   time.Now(),
		Level:       level,
		Message:     msg,
		ServiceName: l.client.config.ServiceName,
		TraceID:     span.TraceID,
		SpanID:      span.SpanID,
		Tags:        merged,
	}

	l.client.addLog(entry)
}

func (l *Logger) log(level LogLevel, msg string, tags map[string]interface{}) {
	entry := LogEntry{
		Timestamp:   time.Now(),
		Level:       level,
		Message:     msg,
		ServiceName: l.client.config.ServiceName,
		Tags:        tags,
	}

	l.client.addLog(entry)
}

func mergeTags(tags []map[string]interface{}) map[string]interface{} {
	if len(tags) == 0 {
		return nil
	}
	result := make(map[string]interface{})
	for _, t := range tags {
		for k, v := range t {
			result[k] = v
		}
	}
	return result
}
