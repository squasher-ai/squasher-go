package squasher

import (
	"log/slog"
	"net/http"
	"strings"
)

type Option func(*transportConfig)

type transportConfig struct {
	endpoint   string
	client     *http.Client
	bufferSize int
	level      slog.Leveler
}

func defaultTransportConfig() transportConfig {
	return transportConfig{
		endpoint:   DefaultEndpoint,
		client:     &http.Client{Timeout: defaultRequestTimeout},
		bufferSize: defaultBufferSize,
		level:      slog.LevelDebug,
	}
}

func WithEndpoint(endpoint string) Option {
	return func(cfg *transportConfig) {
		trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
		if trimmed != "" {
			cfg.endpoint = trimmed
		}
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(cfg *transportConfig) {
		if client != nil {
			cfg.client = client
		}
	}
}

func WithBufferSize(size int) Option {
	return func(cfg *transportConfig) {
		if size > 0 {
			cfg.bufferSize = size
		}
	}
}

func WithLevel(level slog.Leveler) Option {
	return func(cfg *transportConfig) {
		if level != nil {
			cfg.level = level
		}
	}
}
