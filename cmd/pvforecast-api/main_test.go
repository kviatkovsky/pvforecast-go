package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestLoad(t *testing.T) {
	t.Parallel()
	def, err := load(env())
	if err != nil {
		t.Fatal(err)
	}
	if def.port != "8080" || def.cacheTTL != 15*time.Minute || def.maxInFlight != 32 || !def.logJSON || def.logLevel != slog.LevelInfo {
		t.Errorf("defaults = %+v", def)
	}
	got, err := load(env("PORT", "9000", "CACHE_TTL", "5m", "CACHE_SIZE", "10", "MAX_IN_FLIGHT", "4",
		"REQUEST_TIMEOUT", "3s", "LOG_LEVEL", "debug", "LOG_FORMAT", "text", "OPENMETEO_URL", "http://om.local"))
	if err != nil {
		t.Fatal(err)
	}
	want := settings{
		port: "9000", logLevel: slog.LevelDebug, cacheTTL: 5 * time.Minute, cacheSize: 10,
		maxInFlight: 4, requestTimeout: 3 * time.Second, openMeteoURL: "http://om.local",
	}
	if got != want {
		t.Errorf("load = %+v, want %+v", got, want)
	}

	_, err = load(env("PORT", "http", "CACHE_TTL", "-1s", "CACHE_SIZE", "0", "LOG_LEVEL", "loud", "LOG_FORMAT", "xml", "REQUEST_TIMEOUT", "soon"))
	for _, key := range []string{"PORT", "CACHE_TTL", "CACHE_SIZE", "LOG_LEVEL", "LOG_FORMAT", "REQUEST_TIMEOUT"} {
		if err == nil || !strings.Contains(err.Error(), key+"=") {
			t.Errorf("error does not report %s: %v", key, err)
		}
	}
}

// TestServeAndShutdown runs the real server on a free port, makes requests
// over TCP and checks that cancellation shuts it down cleanly.
func TestServeAndShutdown(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- run(ctx, env("PORT", "0", "LOG_LEVEL", "error"), ready) }()

	var addr string
	select {
	case addr = <-ready:
	case err := <-done:
		t.Fatalf("server exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}
	addr = strings.Replace(addr, "[::]", "127.0.0.1", 1)

	for _, path := range []string{"/healthz", "/openapi.yaml"} {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(body) == 0 {
			t.Errorf("%s: %d %q", path, resp.StatusCode, body)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	t.Parallel()
	if err := run(t.Context(), env("MAX_IN_FLIGHT", "-3"), nil); err == nil || !strings.Contains(err.Error(), "MAX_IN_FLIGHT") {
		t.Errorf("err = %v", err)
	}
}
