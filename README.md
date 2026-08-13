# Squasher Go slog Handler

This repository is the public distribution of the Go SDK maintained in the Squasher monorepo.

Install with:

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
	handler := squasher.NewHandler(
		"sq_pk_your_key",
		"your-project-id",
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

To send to a different host during local development:

```go
handler := squasher.NewHandler(
	"sq_pk_your_key",
	"your-project-id",
	squasher.WithEndpoint("http://localhost:3001"),
)
```
