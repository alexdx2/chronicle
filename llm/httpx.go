package llm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryBackoff is the wait before the next attempt after a transport error or
// a 5xx other than 503; such failures get three attempts in all.
var retryBackoff = []time.Duration{time.Second, 3 * time.Second}

// rateBackoff is the wait before the next attempt after 429 or 503 when the
// server sent no usable Retry-After; rate limits get six attempts in all. A
// per-minute window (OpenRouter's new-account limit is 20 requests a minute)
// outlasts the short schedule.
var rateBackoff = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second, 60 * time.Second}

// maxRetryAfter caps a server-requested wait.
const maxRetryAfter = 120 * time.Second

// retryAfter parses a Retry-After header (integer seconds or an HTTP date).
// It reports false when the header is absent, malformed or not positive.
func retryAfter(h string, now time.Time) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.Atoi(h); err == nil {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(h); err == nil {
		d = t.Sub(now)
	} else {
		return 0, false
	}
	if d <= 0 {
		return 0, false
	}
	if d > maxRetryAfter {
		d = maxRetryAfter
	}
	return d, true
}

// postJSON posts body to endpoint, retrying transport errors, 429 and 5xx.
// 429 and 503 wait for Retry-After (else rateBackoff) and get six attempts;
// other failures use retryBackoff and get three. headers are applied to every
// attempt. sleep is a test hook; nil means a context-aware timer. It returns
// the response body and status of the first non-retryable response, or the
// last error when every attempt failed.
func postJSON(ctx context.Context, hc *http.Client, sleep func(time.Duration), name, endpoint string, headers map[string]string, body []byte) ([]byte, int, error) {
	var lastErr error
	var delay time.Duration // wait before the next attempt
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if err := waitFor(ctx, sleep, delay); err != nil {
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
		short := func(e error) bool { // record a short-schedule failure; false when out of attempts
			lastErr = e
			if attempt+1 >= len(retryBackoff)+1 {
				return false
			}
			delay = retryBackoff[attempt]
			return true
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			if !short(fmt.Errorf("llm: %s: %w", name, stripURL(err))) {
				return nil, 0, lastErr
			}
			continue
		}
		raw, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil {
			if !short(fmt.Errorf("llm: %s: read response: %w", name, rerr)) {
				return nil, 0, lastErr
			}
			continue
		}
		switch {
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
			lastErr = fmt.Errorf("llm: %s: status %d: %s", name, resp.StatusCode, snippet(raw))
			if attempt >= len(rateBackoff) {
				return nil, 0, lastErr
			}
			if d, ok := retryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
				delay = d
			} else {
				delay = rateBackoff[attempt]
			}
		case resp.StatusCode >= 500:
			if !short(fmt.Errorf("llm: %s: status %d: %s", name, resp.StatusCode, snippet(raw))) {
				return nil, 0, lastErr
			}
		default:
			return raw, resp.StatusCode, nil
		}
	}
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
