package squasher

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (d *dispatcher) send(ctx context.Context, payload eventPayload) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint+"/v1/ingest/"+d.projectID, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-squasher-key", d.apiKey)
		response, err := d.client.Do(req)
		wait := time.Duration(1<<attempt) * time.Second
		if err == nil {
			status := response.StatusCode
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if status >= 200 && status < 300 {
				return
			}
			if status == 401 {
				d.mu.Lock()
				d.disabled = true
				d.mu.Unlock()
				return
			}
			if status == 429 {
				// The worker is sequential: this also holds every later event.
				wait = retryAfter(response.Header.Get("Retry-After"))
				timer := time.NewTimer(wait)
				select {
				case <-timer.C:
				case <-d.stop:
					timer.Stop()
					return
				}
				if attempt >= maxRetries {
					return
				}
				continue
			}
			if status < 500 {
				return
			}
		}
		if attempt >= maxRetries {
			return
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-d.stop:
			timer.Stop()
			return
		}
	}
}

// Rust parses delay-seconds as u64 and allows a leading "+". A larger value is invalid.
var secondsPattern = regexp.MustCompile(`^\+?[0-9]+$`)

func retryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	wait := 10 * time.Second
	if secondsPattern.MatchString(value) {
		seconds, err := strconv.ParseUint(strings.TrimPrefix(value, "+"), 10, 64)
		if err != nil {
			return wait
		}
		// Cap before multiplication so large server values cannot overflow.
		if seconds >= 60 {
			return time.Minute
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := time.Parse(http.TimeFormat, value); err == nil && date.UTC().Format(http.TimeFormat) == value {
		wait = time.Until(date)
	}
	if wait < 0 {
		return 0
	}
	if wait > time.Minute {
		return time.Minute
	}
	return wait
}
