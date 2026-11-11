# Squasher Go slog Handler

This repository is the public distribution of the Go SDK maintained in the Squasher monorepo.
Do not edit these files here; changes come from the monorepo source.

This package is for Go applications that use `log/slog`. It requires Go 1.21 or later. Run this
command in your Go module (the folder with `go.mod`). For other languages, see the
[quickstart](https://docs.squasher.ai/quickstart).

```bash
go get github.com/squasher-ai/squasher-go
```

Usage:

```go
package main

import (
	"context"
	"log/slog"

	squasher "github.com/squasher-ai/squasher-go"
)

func main() {
	sampling := squasher.DefaultSamplingConfig()
	sampling.SuccessRate = 0.1
	handler := squasher.NewHandler(
		"sq_pk_your_key",
		"your-project-id",
		squasher.WithSampling(sampling),
	)
	defer handler.Close()

	logger := slog.New(handler)
	logger.Error(
		"hello from go",
		slog.String("service", "api"),
		slog.Int("attempt", 2),
	)

	slog.New(
		handler.WithGroup("http").WithAttrs([]slog.Attr{
			slog.String("method", "POST"),
		}),
	).Error("request failed", slog.Int("status", 500))

	_ = handler.Flush(context.Background())
}
```

Attributes keep their number and boolean types. Use `trace_id` and `span_id` attributes for log correlation. Errors and slow records stay at full fidelity by default.

To send to a different host during local development:

```go
handler := squasher.NewHandler(
	"sq_pk_your_key",
	"your-project-id",
	squasher.WithEndpoint("http://localhost:3001"),
)
```

## Delivery

- A 2xx response is not sent again. Delivery is at-least-once: a network error or timeout can repeat a batch the server already accepted.
- HTTP 429 waits for `Retry-After` (whole seconds or an HTTP date, at most 60 seconds; 10 seconds when the value is missing or invalid). The worker holds later events during the wait.
- `Close()` waits at most 5 seconds. `Shutdown(ctx)` uses your own limit. At the limit the SDK cancels the in-flight request and the wait, and drops what remains. `Flush(ctx)` returns when its context ends.
