package llm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// retryBackoff is the wait before attempt 2 and attempt 3.
var retryBackoff = []time.Duration{time.Second, 3 * time.Second}

// postJSON posts body to endpoint up to three times, retrying transport
// errors, 429 and 5xx. headers are applied to every attempt. sleep is a test
// hook; nil means a context-aware timer. It returns the last response body and
// status, or the last error when every attempt failed to reach the server.
func postJSON(ctx context.Context, hc *http.Client, sleep func(time.Duration), name, endpoint string, headers map[string]string, body []byte) ([]byte, int, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if err := waitFor(ctx, sleep, retryBackoff[attempt-1]); err != nil {
				return nil, 0, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, 0, fmt.Errorf("llm: %s: %w", name, stripURL(err))
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			lastErr = fmt.Errorf("llm: %s: %w", name, stripURL(err))
			continue
		}
		raw, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil {
			lastErr = fmt.Errorf("llm: %s: read response: %w", name, rerr)
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("llm: %s: status %d: %s", name, resp.StatusCode, snippet(raw))
			continue
		}
		return raw, resp.StatusCode, nil
	}
	return nil, 0, lastErr
}

func waitFor(ctx context.Context, sleep func(time.Duration), d time.Duration) error {
	if sleep != nil {
		sleep(d)
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
