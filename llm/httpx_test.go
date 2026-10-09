package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func postTest(t *testing.T, h http.HandlerFunc, ctx context.Context, sleep func(time.Duration)) ([]byte, int, error) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return postJSON(ctx, srv.Client(), sleep, "test", srv.URL, nil, []byte(`{}`))
}

func TestPostJSON_RetryAfterSeconds(t *testing.T) {
	calls := 0
	var slept []time.Duration
	raw, status, err := postTest(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "7")
			http.Error(w, "limited", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}, context.Background(), func(d time.Duration) { slept = append(slept, d) })
	if err != nil || status != 200 || string(raw) != "ok" {
		t.Fatalf("raw=%s status=%d err=%v", raw, status, err)
	}
	if len(slept) != 1 || slept[0] != 7*time.Second {
		t.Errorf("slept=%v", slept)
	}
}

func TestPostJSON_RetryAfterCapped(t *testing.T) {
	calls := 0
	var slept []time.Duration
	_, _, err := postTest(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "3600")
			http.Error(w, "limited", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}, context.Background(), func(d time.Duration) { slept = append(slept, d) })
	if err != nil || len(slept) != 1 || slept[0] != 120*time.Second {
		t.Errorf("err=%v slept=%v", err, slept)
	}
}

func TestRetryAfter_HTTPDate(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d, ok := retryAfter(now.Add(30*time.Second).UTC().Format(http.TimeFormat), now)
	if !ok || d != 30*time.Second {
		t.Errorf("d=%v ok=%v", d, ok)
	}
	for _, bad := range []string{"", "soon", "0", "-5", now.Add(-time.Minute).UTC().Format(http.TimeFormat)} {
		if _, ok := retryAfter(bad, now); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPostJSON_429WithoutHeaderThenGivesUpAfterSix(t *testing.T) {
	calls := 0
	var slept []time.Duration
	_, _, err := postTest(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "Rate limit exceeded: new-account-rpm", http.StatusTooManyRequests)
	}, context.Background(), func(d time.Duration) { slept = append(slept, d) })
	if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "new-account-rpm") {
		t.Fatalf("err=%v", err)
	}
	want := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second, 60 * time.Second}
	if calls != 6 || len(slept) != len(want) {
		t.Fatalf("calls=%d slept=%v", calls, slept)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Errorf("slept=%v want %v", slept, want)
		}
	}
}

func TestPostJSON_500GivesUpAfterThree(t *testing.T) {
	calls := 0
	var slept []time.Duration
	_, _, err := postTest(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "boom", http.StatusInternalServerError)
	}, context.Background(), func(d time.Duration) { slept = append(slept, d) })
	if err == nil || !strings.Contains(err.Error(), "500") || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if len(slept) != 2 || slept[0] != time.Second || slept[1] != 3*time.Second {
		t.Errorf("slept=%v", slept)
	}
}

func TestPostJSON_CancelDuringLongWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	done := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "120")
		http.Error(w, "limited", http.StatusTooManyRequests)
		go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	}))
	defer srv.Close()
	go func() {
		_, _, err := postJSON(ctx, srv.Client(), nil, "test", srv.URL, nil, []byte(`{}`))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not abort on cancellation")
	}
}
