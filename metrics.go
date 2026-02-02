package omnipulse

import (
	"time"
)

// MetricType represents the type of metric
type MetricType string

const (
	MetricTypeCounter   MetricType = "counter"
	MetricTypeGauge     MetricType = "gauge"
	MetricTypeHistogram MetricType = "histogram"
)

// MetricData represents a metric data point
type MetricData struct {
	Name        string                 `json:"name"`
	Type        MetricType             `json:"type"`
	Value       float64                `json:"value"`
	Timestamp   time.Time              `json:"timestamp"`
	ServiceName string                 `json:"service_name,omitempty"`
	Tags        map[string]string      `json:"tags,omitempty"`
	Dimensions  map[string]interface{} `json:"dimensions,omitempty"`
}

// Metrics provides metrics collection functionality
type Metrics struct {
	client *Client
}

func newMetrics(c *Client) *Metrics {
	return &Metrics{client: c}
}

// Counter increments a counter metric
func (m *Metrics) Counter(name string, value float64, tags ...map[string]string) {
	m.record(name, MetricTypeCounter, value, mergeTags2(tags))
}

// Gauge records a gauge metric
func (m *Metrics) Gauge(name string, value float64, tags ...map[string]string) {
	m.record(name, MetricTypeGauge, value, mergeTags2(tags))
}

// Histogram records a histogram metric
func (m *Metrics) Histogram(name string, value float64, tags ...map[string]string) {
	m.record(name, MetricTypeHistogram, value, mergeTags2(tags))
}

// RecordDuration records a duration in milliseconds
func (m *Metrics) RecordDuration(name string, duration time.Duration, tags ...map[string]string) {
	m.record(name, MetricTypeHistogram, float64(duration.Milliseconds()), mergeTags2(tags))
}

// Increment increments a counter by 1
func (m *Metrics) Increment(name string, tags ...map[string]string) {
	m.Counter(name, 1, tags...)
}

// Decrement decrements a counter by 1
func (m *Metrics) Decrement(name string, tags ...map[string]string) {
	m.Counter(name, -1, tags...)
}

func (m *Metrics) record(name string, metricType MetricType, value float64, tags map[string]string) {
	metric := MetricData{
		Name:        name,
		Type:        metricType,
		Value:       value,
		Timestamp:   time.Now(),
		ServiceName: m.client.config.ServiceName,
		Tags:        tags,
	}

	m.client.addMetric(metric)
}

func mergeTags2(tags []map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	result := make(map[string]string)
	for _, t := range tags {
		for k, v := range t {
			result[k] = v
		}
	}
	return result
}
