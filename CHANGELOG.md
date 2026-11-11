# Changelog

## Unreleased

## 0.3.0 - 2026-11-11

### Changed

- `Close()` waits at most 5 seconds. New `Shutdown(ctx)` takes your own limit. At the limit the SDK cancels the in-flight request and the Retry-After wait, and drops what remains.
- `Flush(ctx)` returns when the context ends and starts no goroutine that outlives the call.

### Fixed

- A 2xx response is not sent again. Delivery is at-least-once: a network error or timeout can repeat a batch the server already accepted. Permanent 4xx responses are dropped.
- HTTP 429 waits for `Retry-After` (whole seconds or an IMF-fixdate HTTP date, at most 60 seconds). A missing or invalid value waits 10 seconds, the same as the Rust SDK. The worker holds later events during the wait. Each handler has its own gate.
