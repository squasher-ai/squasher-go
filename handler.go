package squasher

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	DefaultEndpoint       = "https://ingest.squasher.ai"
	SDKName               = "squasher-go"
	SDKVersion            = "0.3.0"
	defaultBufferSize     = 100
	defaultRequestTimeout = 5 * time.Second
	maxRetries            = 3
)

type SquasherHandler struct {
	dispatcher *dispatcher
	level      slog.Leveler
	baseAttrs  map[string]any
	groups     []string
	sampling   SamplingConfig
}

type dispatcher struct {
	apiKey    string
	projectID string
	endpoint  string
	client    *http.Client
	queue     chan eventPayload
	done      chan struct{}
	stop      chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	pending   int
	idle      chan struct{}
	mu        sync.Mutex
	closed    bool
	disabled  bool
	closeOnce sync.Once
	stopOnce  sync.Once
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
		sampling:   cfg.sampling,
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
	payload := buildPayload(record, h.baseAttrs, h.groups)
	if shouldSample(payload, h.sampling) {
		h.dispatcher.enqueue(payload)
	}
	return nil
}

func (h *SquasherHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &SquasherHandler{
		dispatcher: h.dispatcher,
		level:      h.level,
		baseAttrs:  cloneAttrs(h.baseAttrs),
		groups:     append([]string(nil), h.groups...),
		sampling:   h.sampling,
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
		sampling:   h.sampling,
	}
}

func (h *SquasherHandler) Flush(ctx context.Context) error {
	if h.dispatcher == nil {
		return nil
	}
	return h.dispatcher.flush(ctx)
}

func (h *SquasherHandler) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.Shutdown(ctx)
}

func (h *SquasherHandler) Shutdown(ctx context.Context) error {
	if h.dispatcher == nil {
		return nil
	}
	return h.dispatcher.shutdown(ctx)
}

func newDispatcher(apiKey, projectID string, cfg transportConfig) *dispatcher {
	dispatcher := &dispatcher{
		apiKey:    apiKey,
		projectID: projectID,
		endpoint:  cfg.endpoint,
		client:    cfg.client,
		queue:     make(chan eventPayload, cfg.bufferSize),
		done:      make(chan struct{}),
		stop:      make(chan struct{}),
		idle:      make(chan struct{}),
	}
	close(dispatcher.idle)
	dispatcher.ctx, dispatcher.cancel = context.WithCancel(context.Background())
	go dispatcher.run()
	return dispatcher
}

func (d *dispatcher) enqueue(payload eventPayload) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.disabled {
		return
	}

	if d.pending == 0 {
		d.idle = make(chan struct{})
	}
	d.pending++
	select {
	case d.queue <- payload:
	default:
		d.pending--
		if d.pending == 0 {
			close(d.idle)
		}
	}
}

func (d *dispatcher) flush(ctx context.Context) error {
	d.mu.Lock()
	idle := d.idle
	d.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *dispatcher) shutdown(ctx context.Context) error {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		close(d.queue)
		d.mu.Unlock()
	})
	select {
	case <-d.done:
		return nil
	case <-ctx.Done():
		d.stopOnce.Do(func() { close(d.stop); d.cancel() })
		return ctx.Err()
	}
}

func (d *dispatcher) run() {
	defer close(d.done)
	defer d.cancel()
	for payload := range d.queue {
		d.mu.Lock()
		disabled := d.disabled
		d.mu.Unlock()
		if !disabled {
			d.send(d.ctx, payload)
		}
		d.mu.Lock()
		d.pending--
		if d.pending == 0 {
			close(d.idle)
		}
		d.mu.Unlock()
	}
}
