package omnipulse

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
)

// FiberMiddleware returns a Fiber middleware for automatic instrumentation
func FiberMiddleware(client *Client) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Skip health check endpoints
		path := c.Path()
		if path == "/health" || path == "/healthz" || path == "/ready" || path == "/readyz" {
			return c.Next()
		}

		// Start span
		span := client.Tracer().StartSpan(
			fmt.Sprintf("%s %s", c.Method(), path),
			WithAttributes(map[string]interface{}{
				"http.method":      c.Method(),
				"http.url":         c.OriginalURL(),
				"http.route":       c.Route().Path,
				"http.user_agent":  c.Get("User-Agent"),
				"http.remote_addr": c.IP(),
			}),
		)

		// Store span in context for downstream logging
		c.Locals("omnipulse_span", span)
		c.Locals("omnipulse_trace_id", span.TraceID)

		start := time.Now()

		// Execute handler
		err := c.Next()

		duration := time.Since(start)
		statusCode := c.Response().StatusCode()

		// Set response attributes
		span.SetAttribute("http.status_code", statusCode)
		span.SetAttribute("http.response_size", len(c.Response().Body()))

		if err != nil {
			span.SetStatus(SpanStatusError, err.Error())
			span.SetAttribute("error", true)
			span.SetAttribute("error.message", err.Error())
		} else if statusCode >= 400 {
			span.SetStatus(SpanStatusError, fmt.Sprintf("HTTP %d", statusCode))
			span.SetAttribute("error", true)
		}

		span.End()

		// Record metrics
		client.Metrics().RecordDuration("http.request.duration", duration, map[string]string{
			"method":      c.Method(),
			"route":       c.Route().Path,
			"status_code": fmt.Sprintf("%d", statusCode),
		})
		client.Metrics().Increment("http.request.count", map[string]string{
			"method":      c.Method(),
			"route":       c.Route().Path,
			"status_code": fmt.Sprintf("%d", statusCode),
		})

		return err
	}
}

// GetSpanFromFiber retrieves the current span from Fiber context
func GetSpanFromFiber(c *fiber.Ctx) *Span {
	span, ok := c.Locals("omnipulse_span").(*Span)
	if !ok {
		return nil
	}
	return span
}

// GetTraceIDFromFiber retrieves the current trace ID from Fiber context
func GetTraceIDFromFiber(c *fiber.Ctx) string {
	traceID, ok := c.Locals("omnipulse_trace_id").(string)
	if !ok {
		return ""
	}
	return traceID
}

// LogFromFiber logs a message with the current request's trace context
func LogFromFiber(c *fiber.Ctx, client *Client, level LogLevel, msg string, tags ...map[string]interface{}) {
	span := GetSpanFromFiber(c)
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
