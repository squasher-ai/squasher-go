package squasher

import (
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"
)

type Option func(*transportConfig)

type transportConfig struct {
	endpoint   string
	client     *http.Client
	bufferSize int
	level      slog.Leveler
	sampling   SamplingConfig
}

type SamplingConfig struct {
	SuccessRate          float64
	ErrorRate            float64
	SlowRate             float64
	SlowThreshold        time.Duration
	AlwaysSampleUserIDs  []string
	AlwaysSampleReleases []string
}

func DefaultSamplingConfig() SamplingConfig {
	return SamplingConfig{
		SuccessRate:   1,
		ErrorRate:     1,
		SlowRate:      1,
		SlowThreshold: time.Second,
	}
}

func defaultTransportConfig() transportConfig {
	return transportConfig{
		endpoint:   DefaultEndpoint,
		client:     &http.Client{Timeout: defaultRequestTimeout},
		bufferSize: defaultBufferSize,
		level:      slog.LevelDebug,
		sampling:   DefaultSamplingConfig(),
	}
}

func WithSampling(sampling SamplingConfig) Option {
	return func(cfg *transportConfig) {
		cfg.sampling = normalizeSampling(sampling)
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

func normalizeSampling(sampling SamplingConfig) SamplingConfig {
	sampling.SuccessRate = clampRate(sampling.SuccessRate)
	sampling.ErrorRate = clampRate(sampling.ErrorRate)
	sampling.SlowRate = clampRate(sampling.SlowRate)
	if sampling.SlowThreshold < 0 {
		sampling.SlowThreshold = 0
	}
	return sampling
}

func clampRate(rate float64) float64 {
	if math.IsNaN(rate) {
		return 1
	}
	return min(1, max(0, rate))
}
