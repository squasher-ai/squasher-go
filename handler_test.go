package squasher

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type capturedRequest struct {
	path    string
	headers map[string]string
	body    map[string]any
}

type recordingServer struct {
	server   *httptest.Server
	delay    time.Duration
	mu       sync.Mutex
	requests []capturedRequest
}

func newRecordingServer(delay time.Duration) *recordingServer {
	rs := &recordingServer{delay: delay}
	rs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		body := map[string]any{}
		if err := json.Unmarshal(payload, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		rs.mu.Lock()
		rs.requests = append(rs.requests, capturedRequest{
			path:    r.URL.Path,
			headers: map[string]string{"content-type": r.Header.Get("Content-Type"), "x-squasher-key": r.Header.Get("x-squasher-key")},
			body:    body,
		})
		rs.mu.Unlock()

		if rs.delay > 0 {
			time.Sleep(rs.delay)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	return rs
}

func (rs *recordingServer) close() {
	rs.server.Close()
}

func (rs *recordingServer) url() string {
	return rs.server.URL
}

func (rs *recordingServer) snapshot() []capturedRequest {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	copied := make([]capturedRequest, len(rs.requests))
	copy(copied, rs.requests)
	return copied
}

func TestHandlePostsExpectedPayload(t *testing.T) {
	server := newRecordingServer(0)
	t.Cleanup(server.close)

	handler := NewHandler("sq_pk_test", "project-123", WithEndpoint(server.url()))
	t.Cleanup(func() {
		if err := handler.Close(); err != nil {
			t.Fatalf("close handler: %v", err)
		}
	})

	logger := slog.New(
		handler.WithAttrs([]slog.Attr{
			slog.String("service", "checkout"),
			slog.Group("http", slog.String("method", "POST")),
		}),
	)
	logger.Error("test go error", slog.String("request_id", "req-123"), slog.Int("attempt", 2))

	if err := handler.Flush(context.Background()); err != nil {
		t.Fatalf("flush handler: %v", err)
	}

	requests := server.snapshot()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	request := requests[0]
	if request.path != "/v1/ingest/project-123" {
		t.Fatalf("unexpected path: %s", request.path)
	}
	if request.headers["x-squasher-key"] != "sq_pk_test" {
		t.Fatalf("unexpected api key header: %q", request.headers["x-squasher-key"])
	}
	if request.body["message"] != "test go error" {
		t.Fatalf("unexpected message: %#v", request.body["message"])
	}
	if request.body["level"] != "error" {
		t.Fatalf("unexpected level: %#v", request.body["level"])
	}

	sdk := request.body["sdk"].(map[string]any)
	if sdk["name"] != "squasher-go" || sdk["version"] != "0.1.0" {
		t.Fatalf("unexpected sdk payload: %#v", sdk)
	}

	attrs := request.body["attrs"].(map[string]any)
	httpAttrs := attrs["http"].(map[string]any)
	if attrs["service"] != "checkout" || attrs["request_id"] != "req-123" || httpAttrs["method"] != "POST" {
		t.Fatalf("unexpected attrs payload: %#v", attrs)
	}
	if attrs["attempt"] != float64(2) {
		t.Fatalf("unexpected numeric attr: %#v", attrs["attempt"])
	}

	tags := request.body["tags"].(map[string]any)
	if tags["http.method"] != "POST" || tags["request_id"] != "req-123" {
		t.Fatalf("unexpected tag payload: %#v", tags)
	}
}

func TestWithGroupScopesOnlyFutureAttributes(t *testing.T) {
	server := newRecordingServer(0)
	t.Cleanup(server.close)

	handler := NewHandler("sq_pk_test", "project-123", WithEndpoint(server.url()))
	t.Cleanup(func() {
		if err := handler.Close(); err != nil {
			t.Fatalf("close handler: %v", err)
		}
	})

	grouped := handler.WithAttrs([]slog.Attr{slog.String("service", "payments")}).WithGroup("http")
	logger := slog.New(grouped)
	logger.Info("request completed", slog.String("method", "GET"), slog.Int("status", 201))

	if err := handler.Flush(context.Background()); err != nil {
		t.Fatalf("flush handler: %v", err)
	}

	attrs := server.snapshot()[0].body["attrs"].(map[string]any)
	if attrs["service"] != "payments" {
		t.Fatalf("expected ungrouped service attr, got %#v", attrs)
	}

	httpAttrs := attrs["http"].(map[string]any)
	if httpAttrs["method"] != "GET" || httpAttrs["status"] != float64(201) {
		t.Fatalf("unexpected grouped attrs: %#v", httpAttrs)
	}
}

func TestEnabledRespectsConfiguredLevel(t *testing.T) {
	handler := NewHandler("sq_pk_test", "project-123", WithLevel(slog.LevelWarn))
	t.Cleanup(func() {
		if err := handler.Close(); err != nil {
			t.Fatalf("close handler: %v", err)
		}
	})

	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("expected info level to be disabled")
	}
	if !handler.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("expected error level to be enabled")
	}
}

func TestHandleIsNonBlockingWhileWorkerSendsInBackground(t *testing.T) {
	server := newRecordingServer(300 * time.Millisecond)
	t.Cleanup(server.close)

	handler := NewHandler("sq_pk_test", "project-123", WithEndpoint(server.url()))
	t.Cleanup(func() {
		if err := handler.Close(); err != nil {
			t.Fatalf("close handler: %v", err)
		}
	})

	startedAt := time.Now()
	record := slog.NewRecord(time.Now(), slog.LevelError, "slow request", 0)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("handle record: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed >= 100*time.Millisecond {
		t.Fatalf("expected async handle, took %s", elapsed)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := handler.Flush(ctx); err != nil {
		t.Fatalf("flush handler: %v", err)
	}
	if len(server.snapshot()) != 1 {
		t.Fatalf("expected background worker to send request")
	}
}
