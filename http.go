package omnipulse

import (
	"fmt"
	"net/http"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    int
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += n
	return n, err
}

// HTTPMiddleware returns a standard net/http middleware for automatic instrumentation
// Usage: http.Handle("/", omnipulse.HTTPMiddleware(client)(yourHandler))
func HTTPMiddleware(client *Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip health check endpoints
			path := r.URL.Path
			if path == "/health" || path == "/healthz" || path == "/ready" || path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}

			// Extract or generate trace context
			traceID := r.Header.Get("X-OmniPulse-Trace-ID")
			parentSpanID := r.Header.Get("X-OmniPulse-Span-ID")

			opts := []SpanOption{
				WithAttributes(map[string]interface{}{
					"http.method":      r.Method,
					"http.url":         r.URL.String(),
					"http.host":        r.Host,
					"http.user_agent":  r.UserAgent(),
					"http.remote_addr": r.RemoteAddr,
				}),
			}

			if traceID != "" {
				opts = append(opts, WithTraceID(traceID))
			}
			if parentSpanID != "" {
				opts = append(opts, WithParentSpanID(parentSpanID))
			}

			// Start span
			span := client.Tracer().StartSpan(
				fmt.Sprintf("%s %s", r.Method, path),
				opts...,
			)

			// Set trace context in request context
			ctx := ContextWithSpan(r.Context(), span)
			r = r.WithContext(ctx)

			// Add trace headers to response
			w.Header().Set("X-OmniPulse-Trace-ID", span.TraceID)

			start := time.Now()

			// Wrap response writer to capture status
			rw := newResponseWriter(w)

			// Execute handler
			next.ServeHTTP(rw, r)

			duration := time.Since(start)

			// Set response attributes
			span.SetAttribute("http.status_code", rw.statusCode)
			span.SetAttribute("http.response_size", rw.written)

			if rw.statusCode >= 400 {
				span.SetStatus(SpanStatusError, fmt.Sprintf("HTTP %d", rw.statusCode))
				span.SetAttribute("error", true)
			}

			span.End()

			// Record metrics
			client.Metrics().RecordDuration("http.request.duration", duration, map[string]string{
				"method":      r.Method,
				"path":        path,
				"status_code": fmt.Sprintf("%d", rw.statusCode),
			})
			client.Metrics().Increment("http.request.count", map[string]string{
				"method":      r.Method,
				"path":        path,
				"status_code": fmt.Sprintf("%d", rw.statusCode),
			})
		})
	}
}

// HTTPHandlerFunc returns a middleware for http.HandlerFunc
// Usage: http.HandleFunc("/", omnipulse.HTTPHandlerFunc(client, yourHandlerFunc))
func HTTPHandlerFunc(client *Client, handler http.HandlerFunc) http.HandlerFunc {
	return HTTPMiddleware(client)(http.HandlerFunc(handler)).ServeHTTP
}

// GetSpanFromContext retrieves the current span from context
func GetSpanFromContext(r *http.Request) *Span {
	return SpanFromContext(r.Context())
}

// GetTraceIDFromContext retrieves the current trace ID from context
func GetTraceIDFromContext(r *http.Request) string {
	span := SpanFromContext(r.Context())
	if span != nil {
		return span.TraceID
	}
	return ""
}

// LogFromRequest logs a message with the current request's trace context
func LogFromRequest(r *http.Request, client *Client, level LogLevel, msg string, tags ...map[string]interface{}) {
	span := GetSpanFromContext(r)
	if span != nil {
		client.Logger().WithContext(span, level, msg, tags...)
	} else {
		// Fallback to normal logging
		switch level {
		case LogLevelDebug:
			client.Logger().Debug(msg, tags...)
		case LogLevelInfo:
			client.Logger().Info(msg, tags...)
		case LogLevelWarn:
			client.Logger().Warn(msg, tags...)
		case LogLevelError:
			client.Logger().Error(msg, tags...)
		case LogLevelFatal:
			client.Logger().Fatal(msg, tags...)
		}
	}
}
