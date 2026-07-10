package datadog

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestAPIErrorMessage(t *testing.T) {
	if got := apiErrorMessage([]byte(`{"errors":["bad query","try again"]}`)); got != "bad query; try again" {
		t.Errorf("got %q", got)
	}
	if got := apiErrorMessage([]byte(`plain text error`)); got != "plain text error" {
		t.Errorf("fallback: got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("short: got %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("long: got %q", got)
	}
}

func TestDoRetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			w.Write([]byte(`{"errors":["rate limited"]}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL, "k", "k")
	body, err := c.do("GET", "/test", nil)
	if err != nil {
		t.Fatalf("expected retry to succeed: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("got %q", body)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestDoNoRetryOn400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(400)
		w.Write([]byte(`{"errors":["bad request"]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL, "k", "k")
	_, err := c.do("GET", "/test", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("400 must not retry: got %d calls", calls)
	}
}

func TestDoSendsAuthAndUserAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("DD-API-KEY") != "api" || r.Header.Get("DD-APPLICATION-KEY") != "app" {
			t.Error("missing auth headers")
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL, "api", "app")
	if _, err := c.do("GET", "/test", nil); err != nil {
		t.Fatal(err)
	}
}
