// Command pvforecast-api serves PV forecasts over HTTP.
//
// It is configured through environment variables, so the same binary runs
// locally, in a container and on AWS Lambda (behind the Lambda Web
// Adapter):
//
//	PORT              listen port (default 8080)
//	LOG_LEVEL         debug, info, warn or error (default info)
//	LOG_FORMAT        json or text (default json)
//	CACHE_TTL         weather cache lifetime (default 15m)
//	CACHE_SIZE        maximum cached forecasts (default 1000)
//	MAX_IN_FLIGHT     concurrent forecasts before 503 (default 32)
//	REQUEST_TIMEOUT   per-forecast deadline (default 30s)
//	OPENMETEO_URL     Open-Meteo endpoint (default the public API)
//
// It shuts down gracefully on SIGINT and SIGTERM.
//
// "pvforecast-api healthcheck" probes /healthz on the local PORT and exits
// 0 when it answers 200. The container image uses it for HEALTHCHECK,
// because the distroless base has no curl or shell.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kviatkovsky/pvforecast-go/internal/httpapi"
	"github.com/kviatkovsky/pvforecast-go/weather"
	"github.com/kviatkovsky/pvforecast-go/weather/openmeteo"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(context.Background(), os.Getenv))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Getenv, nil)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pvforecast-api: %v\n", err)
		os.Exit(1)
	}
}

// healthcheck returns 0 if the server on the configured port is healthy.
func healthcheck(ctx context.Context, getenv func(string) string) int {
	cfg, err := load(getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+cfg.port+"/healthz", http.NoBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: HTTP %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

// settings are the parsed environment.
type settings struct {
	port           string
	logLevel       slog.Level
	logJSON        bool
	cacheTTL       time.Duration
	cacheSize      int
	maxInFlight    int
	requestTimeout time.Duration
	openMeteoURL   string
}

func load(getenv func(string) string) (settings, error) {
	s := settings{
		port: "8080", logJSON: true, cacheTTL: 15 * time.Minute, cacheSize: 1000,
		maxInFlight: 32, requestTimeout: 30 * time.Second,
	}
	var errs []error
	str := func(key string, dst *string) {
		if v := getenv(key); v != "" {
			*dst = v
		}
	}
	dur := func(key string, dst *time.Duration) {
		if v := getenv(key); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				errs = append(errs, fmt.Errorf("%s=%q: want a positive duration such as 30s", key, v))
				return
			}
			*dst = d
		}
	}
	num := func(key string, dst *int) {
		if v := getenv(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				errs = append(errs, fmt.Errorf("%s=%q: want a positive integer", key, v))
				return
			}
			*dst = n
		}
	}
	str("PORT", &s.port)
	str("OPENMETEO_URL", &s.openMeteoURL)
	dur("CACHE_TTL", &s.cacheTTL)
	dur("REQUEST_TIMEOUT", &s.requestTimeout)
	num("CACHE_SIZE", &s.cacheSize)
	num("MAX_IN_FLIGHT", &s.maxInFlight)
	if v := getenv("LOG_LEVEL"); v != "" {
		if err := s.logLevel.UnmarshalText([]byte(v)); err != nil {
			errs = append(errs, fmt.Errorf("LOG_LEVEL=%q: want debug, info, warn or error", v))
		}
	}
	switch f := getenv("LOG_FORMAT"); f {
	case "", "json":
	case "text":
		s.logJSON = false
	default:
		errs = append(errs, fmt.Errorf("LOG_FORMAT=%q: want json or text", f))
	}
	if _, err := strconv.ParseUint(s.port, 10, 16); err != nil {
		errs = append(errs, fmt.Errorf("PORT=%q: want a port number", s.port))
	}
	return s, errors.Join(errs...)
}

// run serves until ctx is canceled. If ready is non-nil, the bound address
// is sent on it once the listener is open (used by tests).
func run(ctx context.Context, getenv func(string) string, ready chan<- string) error {
	cfg, err := load(getenv)
	if err != nil {
		return err
	}
	var handler slog.Handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.logLevel})
	if !cfg.logJSON {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.logLevel})
	}
	logger := slog.New(handler)

	opts := []openmeteo.Option{}
	if cfg.openMeteoURL != "" {
		opts = append(opts, openmeteo.WithBaseURL(cfg.openMeteoURL))
	}
	provider := weather.NewCache(openmeteo.New(opts...), cfg.cacheTTL, cfg.cacheSize)

	srv := &http.Server{
		Handler: httpapi.New(httpapi.Config{
			Provider: provider, Logger: logger, Version: version,
			RequestTimeout: cfg.requestTimeout, MaxInFlight: cfg.maxInFlight,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      cfg.requestTimeout + 10*time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(handler, slog.LevelWarn),
	}
	ln, err := net.Listen("tcp", ":"+cfg.port)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	logger.Info("listening", "addr", ln.Addr().String(), "version", version)
	if ready != nil {
		ready <- ln.Addr().String()
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
