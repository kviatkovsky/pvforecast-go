// Package httpapi serves PV forecasts over HTTP.
//
// Routes:
//
//	GET  /v1/forecast    single-array system from query parameters
//	POST /v1/forecast    any system as JSON (the CLI --config format)
//	GET  /healthz        liveness
//	GET  /version        build version
//	GET  /openapi.yaml   OpenAPI 3.1 description
//
// Errors are application/problem+json (RFC 9457).
package httpapi

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/kviatkovsky/pvforecast-go/forecast"
	"github.com/kviatkovsky/pvforecast-go/weather"
)

//go:embed openapi.yaml
var openAPISpec []byte

// Config configures a Server.
type Config struct {
	// Provider supplies weather forecasts; wrap it in a weather.Cache.
	Provider weather.Provider
	// Logger receives one access log line per request. Nil discards.
	Logger *slog.Logger
	// Version is reported by /version.
	Version string
	// RequestTimeout bounds each forecast, including the weather fetch.
	// Zero means 30 s.
	RequestTimeout time.Duration
	// MaxInFlight bounds concurrent forecast requests; more are rejected
	// with 503 instead of queuing. Zero means 32.
	MaxInFlight int
	// Now is the clock for forecast windows. Nil means time.Now.
	Now func() time.Time
}

// Server is an http.Handler that serves the API. It is safe for concurrent
// use.
type Server struct {
	cfg     Config
	slots   chan struct{}
	handler http.Handler
}

// New returns a Server.
func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = 32
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Server{cfg: cfg, slots: make(chan struct{}, cfg.MaxInFlight)}

	mux := http.NewServeMux()
	mux.Handle("GET /v1/forecast", s.limit(http.HandlerFunc(s.forecastQuery)))
	mux.Handle("POST /v1/forecast", s.limit(http.HandlerFunc(s.forecastJSON)))
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /version", s.version)
	mux.HandleFunc("GET /openapi.yaml", s.openAPI)
	s.handler = requestID(s.accessLog(s.recoverer(problemRouting(mux))))
	return s
}

// problemRouting serves the mux's own 404 and 405 responses as problem
// details. A catch-all "/" route would do that for 404, but it would also
// shadow the 405 the mux returns for a known path with the wrong method.
func problemRouting(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			h.ServeHTTP(w, r)
			return
		}
		probe := &headerProbe{header: http.Header{}}
		h.ServeHTTP(probe, r)
		if allow := probe.header.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		writeProblem(w, r, probe.status, http.StatusText(probe.status), "no route for "+r.Method+" "+r.URL.Path, nil)
	})
}

// headerProbe records the status and headers of the mux's fallback handler
// and discards its plain-text body.
type headerProbe struct {
	header http.Header
	status int
}

func (p *headerProbe) Header() http.Header         { return p.header }
func (p *headerProbe) Write(b []byte) (int, error) { return len(b), nil }
func (p *headerProbe) WriteHeader(code int)        { p.status = code }

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) version(w http.ResponseWriter, _ *http.Request) {
	v := s.cfg.Version
	if v == "" {
		v = "dev"
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": v})
}

func (s *Server) openAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(openAPISpec)
}

// limit rejects requests beyond MaxInFlight with 503 and Retry-After, so an
// overloaded instance sheds load instead of queuing until timeouts.
func (s *Server) limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			writeProblem(w, r, http.StatusServiceUnavailable, "Overloaded",
				"too many concurrent forecast requests; retry shortly", nil)
		}
	})
}

type ctxKey struct{}

// requestID propagates X-Request-ID, generating one when absent.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 128 {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// statusRecorder captures the status code for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.cfg.Logger.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
			slog.String("request_id", w.Header().Get("X-Request-ID")),
		)
	})
}

// recoverer turns a panic into a 500 instead of a dropped connection, and
// logs it. http.ErrAbortHandler is re-panicked as net/http expects.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				s.cfg.Logger.LogAttrs(r.Context(), slog.LevelError, "panic",
					slog.Any("value", v), slog.String("stack", string(debug.Stack())))
				writeProblem(w, r, http.StatusInternalServerError, "Internal error", "", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// problem is an RFC 9457 problem details object.
type problem struct {
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Status    int      `json:"status"`
	Detail    string   `json:"detail,omitempty"`
	Errors    []string `json:"errors,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, title, detail string, errs []string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type: "about:blank", Title: title, Status: status, Detail: detail, Errors: errs,
		RequestID: requestIDFrom(r.Context()),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fieldErrors flattens an errors.Join of *forecast.FieldError values into
// one message per field.
func fieldErrors(err error) []string {
	var msgs []string
	var walk func(error)
	walk = func(e error) {
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range joined.Unwrap() {
				walk(inner)
			}
			return
		}
		var fe *forecast.FieldError
		if errors.As(e, &fe) {
			msgs = append(msgs, fe.Field+": "+fe.Msg)
		}
	}
	walk(err)
	return msgs
}
