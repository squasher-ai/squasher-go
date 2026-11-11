package squasher

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeliveryWiring(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	handler := NewHandler("key", "project", WithEndpoint(server.URL))
	start := time.Now()
	slog.New(handler).Error("pending")
	if err := handler.Close(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || time.Since(start) < time.Second {
		t.Fatal("retry did not honor gate")
	}
}

func TestCloseDuringMinutePause(t *testing.T) {
	var calls atomic.Int32
	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		select {
		case received <- struct{}{}:
		default:
		}
	}))
	defer server.Close()
	handler := NewHandler("key", "project", WithEndpoint(server.URL))
	slog.New(handler).Error("pending")
	<-received
	start := time.Now()
	if err := handler.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close: %v", err)
	}
	if time.Since(start) > 5500*time.Millisecond {
		t.Fatal("close exceeded default limit")
	}
	select {
	case <-handler.dispatcher.done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	if calls.Load() != 1 {
		t.Fatal("sent after close")
	}
}

func TestFlushDeadlineAndShutdownCancelRequest(t *testing.T) {
	received := make(chan struct{})
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(received)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	handler := NewHandler("key", "project", WithEndpoint(server.URL))
	slog.New(handler).Error("pending")
	<-received
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if !errors.Is(handler.Flush(ctx), context.Canceled) {
			t.Fatal("flush ignored deadline")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if !errors.Is(handler.Shutdown(ctx), context.DeadlineExceeded) {
		t.Fatal("shutdown ignored deadline")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("request not canceled")
	}
}

func TestRetryAfterStrict(t *testing.T) {
	for _, c := range []struct {
		value string
		want  time.Duration
	}{
		{"2", 2 * time.Second}, {"-1", 10 * time.Second}, {"1.5", 10 * time.Second},
		{"Infinity", 10 * time.Second}, {"garbage", 10 * time.Second}, {"", 10 * time.Second},
		{"120", time.Minute}, {"+5", 5 * time.Second},
		{"9223372036854775808", time.Minute}, {"18446744073709551615", time.Minute},
		{"18446744073709551616", 10 * time.Second}, {"999999999999999999999999", 10 * time.Second},
		{"Sunday, 08-Nov-26 12:00:00 GMT", 10 * time.Second},
	} {
		if got := retryAfter(c.value); got != c.want {
			t.Errorf("%q: %v != %v", c.value, got, c.want)
		}
	}
}
