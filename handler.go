package squasher

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	DefaultEndpoint       = "https://ingest.squasher.ai"
	SDKName               = "squasher-go"
	SDKVersion            = "0.1.0"
	defaultBufferSize     = 100
	defaultRequestTimeout = 5 * time.Second
)

type SquasherHandler struct {
	dispatcher *dispatcher
	level      slog.Leveler
	baseAttrs  map[string]any
	groups     []string
}

type dispatcher struct {
	apiKey    string
	projectID string
	endpoint  string
	client    *http.Client
	queue     chan eventPayload
	done      chan struct{}
	wg        sync.WaitGroup
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

func NewHandler(apiKey, projectID string, opts ...Option) *SquasherHandler {
	cfg := defaultTransportConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	trimmedKey := strings.TrimSpace(apiKey)
	trimmedProjectID := strings.TrimSpace(projectID)
	var sharedDispatcher *dispatcher
	if trimmedKey != "" && trimmedProjectID != "" {
		sharedDispatcher = newDispatcher(trimmedKey, trimmedProjectID, cfg)
	}

	return &SquasherHandler{
		dispatcher: sharedDispatcher,
		level:      cfg.level,
		baseAttrs:  map[string]any{},
	}
}

func (h *SquasherHandler) Enabled(_ context.Context, level slog.Level) bool {
	if h.dispatcher == nil {
		return false
	}
	return level >= h.level.Level()
}

func (h *SquasherHandler) Handle(_ context.Context, record slog.Record) error {
	if h.dispatcher == nil {
		return nil
	}
	h.dispatcher.enqueue(buildPayload(record, h.baseAttrs, h.groups))
	return nil
}

func (h *SquasherHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &SquasherHandler{
		dispatcher: h.dispatcher,
		level:      h.level,
		baseAttrs:  cloneAttrs(h.baseAttrs),
		groups:     append([]string(nil), h.groups...),
	}
	for _, attr := range attrs {
		applyAttr(next.baseAttrs, next.groups, attr)
	}
	return next
}

func (h *SquasherHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &SquasherHandler{
		dispatcher: h.dispatcher,
		level:      h.level,
		baseAttrs:  h.baseAttrs,
		groups:     append(append([]string(nil), h.groups...), name),
	}
}

func (h *SquasherHandler) Flush(ctx context.Context) error {
	if h.dispatcher == nil {
		return nil
	}
	return h.dispatcher.flush(ctx)
}

func (h *SquasherHandler) Close() error {
	if h.dispatcher == nil {
		return nil
	}
	return h.dispatcher.close()
}

func newDispatcher(apiKey, projectID string, cfg transportConfig) *dispatcher {
	dispatcher := &dispatcher{
		apiKey:    apiKey,
		projectID: projectID,
		endpoint:  cfg.endpoint,
		client:    cfg.client,
		queue:     make(chan eventPayload, cfg.bufferSize),
		done:      make(chan struct{}),
	}
	go dispatcher.run()
	return dispatcher
}

func (d *dispatcher) enqueue(payload eventPayload) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}

	d.wg.Add(1)
	select {
	case d.queue <- payload:
	default:
		d.wg.Done()
	}
}

func (d *dispatcher) flush(ctx context.Context) error {
	wait := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(wait)
	}()

	select {
	case <-wait:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *dispatcher) close() error {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		close(d.queue)
		d.mu.Unlock()
		<-d.done
	})
	return nil
}

func (d *dispatcher) run() {
	defer close(d.done)
	for payload := range d.queue {
		d.send(payload)
		d.wg.Done()
	}
}

func (d *dispatcher) send(payload eventPayload) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	request, err := http.NewRequest(
		http.MethodPost,
		d.endpoint+"/v1/ingest/"+d.projectID,
		bytes.NewReader(body),
	)
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-squasher-key", d.apiKey)

	response, err := d.client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
}
