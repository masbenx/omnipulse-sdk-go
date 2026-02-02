# OmniPulse Go SDK

The official OmniPulse SDK for Go applications. Provides logging, distributed tracing, and metrics collection with automatic Fiber framework integration.

## Installation

```bash
go get github.com/masbenx/omnipulse-go
```

## Quick Start

```go
package main

import (
	"github.com/gofiber/fiber/v2"
	omnipulse "github.com/masbenx/omnipulse-go"
)

func main() {
	// Initialize the client
	op, err := omnipulse.New(omnipulse.Config{
		APIUrl:      "https://api.omnipulse.cloud",
		IngestKey:   "your-ingest-key",
		ServiceName: "my-api",
		Environment: "production",
	})
	if err != nil {
		panic(err)
	}
	defer op.Close()

	// Test connectivity
	if err := op.Test(); err != nil {
		panic(err)
	}

	// Create Fiber app with middleware
	app := fiber.New()
	app.Use(omnipulse.FiberMiddleware(op))

	app.Get("/", func(c *fiber.Ctx) error {
		// Log with trace context
		omnipulse.LogFromFiber(c, op, omnipulse.LogLevelInfo, "Handling request")
		return c.SendString("Hello!")
	})

	app.Listen(":3000")
}
```

## Features

### Logging

```go
op.Logger().Info("User logged in", map[string]interface{}{
	"user_id": 123,
	"email":   "user@example.com",
})

op.Logger().Error("Database connection failed", map[string]interface{}{
	"error": err.Error(),
	"host":  "localhost:5432",
})
```

### Tracing

```go
// Start a span
span := op.Tracer().StartSpan("process-order")
span.SetAttribute("order_id", 456)

// Do work...

// Mark span as error if needed
if err != nil {
	span.SetStatus(omnipulse.SpanStatusError, err.Error())
}

// End span (sends to backend)
span.End()
```

### Child Spans

```go
parentSpan := op.Tracer().StartSpan("parent-operation")

childSpan := op.Tracer().StartSpan("child-operation", 
	omnipulse.WithParent(parentSpan),
)
// ... do work
childSpan.End()

parentSpan.End()
```

### Metrics

```go
// Counter
op.Metrics().Counter("orders.created", 1, map[string]string{
	"region": "us-east",
})

// Gauge
op.Metrics().Gauge("queue.length", 42)

// Duration
start := time.Now()
// ... do work
op.Metrics().RecordDuration("operation.duration", time.Since(start))
```

### Fiber Middleware

The middleware automatically:
- Creates spans for each HTTP request
- Records request duration and count metrics
- Captures status codes and errors
- Provides trace context for downstream logging

```go
app.Use(omnipulse.FiberMiddleware(op))

// Access span in handler
app.Get("/user/:id", func(c *fiber.Ctx) error {
	span := omnipulse.GetSpanFromFiber(c)
	span.SetAttribute("user_id", c.Params("id"))
	
	omnipulse.LogFromFiber(c, op, omnipulse.LogLevelInfo, "Fetching user")
	
	return c.JSON(user)
})
```

## Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `APIUrl` | OmniPulse backend URL | `OMNIPULSE_URL` env |
| `IngestKey` | Your ingest key | `OMNIPULSE_INGEST_KEY` env |
| `ServiceName` | Name of your service | - |
| `Environment` | Deployment environment | `production` |
| `BatchSize` | Items to buffer before sending | `100` |
| `FlushInterval` | How often to flush buffer | `5s` |
| `Timeout` | HTTP request timeout | `5s` |
| `Debug` | Enable debug logging | `false` |

## Environment Variables

```bash
export OMNIPULSE_URL=https://api.omnipulse.cloud
export OMNIPULSE_INGEST_KEY=your-key-here
```

## Best Practices

1. **Always call `Close()`** - Ensures all buffered data is sent before exit
2. **Use context logging** - Use `LogFromFiber()` for trace correlation
3. **Set meaningful names** - Use descriptive span names like `"get-user-by-id"`
4. **Add relevant attributes** - Include IDs, counts, and other context
5. **Handle errors** - Mark spans as errors for better debugging

## License

MIT
