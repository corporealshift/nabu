package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetriesOn503ThenSucceeds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		sse(w, "[DONE]")
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", Retries: 1}, srv.Client())
	start := time.Now()
	resp, err := p.Complete(context.Background(), Request{Model: "m"}, nil)
	if err != nil || resp.Content != "ok" {
		t.Fatalf("resp: %+v err: %v", resp, err)
	}
	if atomic.LoadInt32(&n) != 2 {
		t.Fatalf("attempts: %d", n)
	}
	if time.Since(start) < time.Second {
		t.Fatal("expected a 1s backoff before the retry")
	}
}

func TestNoRetryAfterFirstDelta(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"partial"}}]}`)
		sse(w, `{"error":{"message":"server died"}}`)
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", Retries: 3}, srv.Client())
	_, err := p.Complete(context.Background(), Request{Model: "m"}, nil)
	if err == nil || atomic.LoadInt32(&n) != 1 {
		t.Fatalf("must not retry after output started: attempts=%d err=%v", n, err)
	}
}

func TestMaxInFlightSerialises(t *testing.T) {
	var inflight, peak int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inflight, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}]}`)
		sse(w, "[DONE]")
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", MaxInFlight: 1}, srv.Client())
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Complete(context.Background(), Request{Model: "m"}, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak != 1 {
		t.Fatalf("peak concurrency %d, want 1", peak)
	}
}
