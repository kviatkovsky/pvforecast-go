package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kviatkovsky/pvforecast-go/forecast"
	"github.com/kviatkovsky/pvforecast-go/internal/config"
	"github.com/kviatkovsky/pvforecast-go/internal/report"
	"github.com/kviatkovsky/pvforecast-go/irradiance"
	"github.com/kviatkovsky/pvforecast-go/power"
	"github.com/kviatkovsky/pvforecast-go/solarpos"
	"github.com/kviatkovsky/pvforecast-go/weather"
)

// maxBodyBytes bounds POST bodies; a 50-array system is about 5 KB.
const maxBodyBytes = 64 << 10

// Defaults shared by both request forms.
const (
	defaultDays = 2
	defaultDCAC = 1.1
)

// forecastRequest is the POST body: a system in the CLI --config format
// plus forecast options.
type forecastRequest struct {
	config.System

	Days     int    `json:"days"`
	TZ       string `json:"tz"`
	SolarPos string `json:"solarpos"`
	Instant  bool   `json:"instant"`
}

// options are the validated, model-level settings of a request.
type options struct {
	days int
	opt  forecast.Options
}

// badRequestError is an input error, reported as 400 with optional field list.
type badRequestError struct {
	msg    string
	fields []string
}

func (e *badRequestError) Error() string { return e.msg }

func badf(format string, args ...any) error {
	return &badRequestError{msg: fmt.Sprintf(format, args...)}
}

func (s *Server) forecastJSON(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req forecastRequest
	if err := dec.Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "Request too large",
				fmt.Sprintf("the body exceeds %d bytes", tooBig.Limit), nil)
			return
		}
		writeProblem(w, r, http.StatusBadRequest, "Invalid JSON", err.Error(), nil)
		return
	}

	sys, err := req.Build(defaultDCAC)
	if err != nil {
		s.fail(w, r, &badRequestError{msg: err.Error()})
		return
	}
	model := irradiance.Perez
	if req.Model != nil {
		model = *req.Model
	}
	o, err := s.options(model, req.Days, req.TZ, req.SolarPos, req.Instant)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.run(w, r, sys, o)
}

func (s *Server) forecastQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := queryParser{q: q}
	lat := p.float("lat", nil)
	lon := p.float("lon", nil)
	kwp := p.float("kwp", nil)
	tilt := p.float("tilt", ptr(35.0))
	azimuth := p.float("azimuth", ptr(180.0))
	altitude := p.float("altitude", ptr(0.0))
	dcac := p.float("dcac", ptr(defaultDCAC))
	losses := p.float("losses", ptr(-1.0))
	days := p.int("days", defaultDays)
	instant := p.bool("instant")
	if len(p.errs) > 0 {
		s.fail(w, r, &badRequestError{msg: "invalid query parameters", fields: p.errs})
		return
	}
	if !(dcac > 0) {
		s.fail(w, r, badf("dcac %g must be positive", dcac))
		return
	}

	model := irradiance.Perez
	if v := q.Get("model"); v != "" {
		m, err := irradiance.ParseModel(v)
		if err != nil {
			s.fail(w, r, &badRequestError{msg: err.Error()})
			return
		}
		model = m
	}
	o, err := s.options(model, days, q.Get("tz"), q.Get("solarpos"), instant)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	sys := forecast.System{
		Name: "PV system", Lat: lat, Lon: lon, AltitudeM: altitude,
		Arrays:   []forecast.Array{{KWp: kwp, TiltDeg: tilt, AzimuthDeg: azimuth}},
		Inverter: power.Inverter{RatedACW: kwp * 1000 / dcac},
		Losses:   power.DefaultLosses,
	}
	if losses >= 0 {
		sys.Losses = power.Losses{Other: losses}
	}
	s.run(w, r, sys, o)
}

// options validates the settings shared by both request forms.
func (s *Server) options(model irradiance.Model, days int, tz, solarPos string, instant bool) (options, error) {
	if days == 0 {
		days = defaultDays
	}
	if days < 1 || days > forecast.MaxDays {
		return options{}, badf("days %d: want 1–%d", days, forecast.MaxDays)
	}
	opt := forecast.Options{Transposition: model, Instant: instant, Now: s.cfg.Now, Location: time.UTC}
	if tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return options{}, badf("tz %q is not an IANA time zone", tz)
		}
		opt.Location = loc
	}
	switch strings.ToLower(solarPos) {
	case "", "spa":
		opt.SolarPos = solarpos.SPA{}
	case "noaa":
		opt.SolarPos = solarpos.NOAA{}
	default:
		return options{}, badf("solarpos %q: want spa or noaa", solarPos)
	}
	return options{days: days, opt: opt}, nil
}

func (s *Server) run(w http.ResponseWriter, r *http.Request, sys forecast.System, o options) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()
	res, err := forecast.Run(ctx, s.cfg.Provider, sys, o.days, o.opt)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Forecasts change at most hourly, so shared caches may keep them for a
	// few minutes.
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, report.New(&sys, &o.opt, &res))
}

// fail maps an error to a problem response: input errors are 400, upstream
// trouble is 502/503, timeouts are 504. Unexpected errors are logged and
// reported without detail.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var bad *badRequestError
	switch {
	case errors.As(err, &bad):
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", bad.msg, bad.fields)
	case errors.Is(err, forecast.ErrInvalidSystem):
		writeProblem(w, r, http.StatusBadRequest, "Invalid system", "", fieldErrors(err))
	case errors.Is(err, weather.ErrInvalidRequest), errors.Is(err, forecast.ErrInvalidOptions),
		errors.Is(err, irradiance.ErrUnknownModel):
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", err.Error(), nil)
	case errors.Is(err, forecast.ErrNoData):
		writeProblem(w, r, http.StatusBadGateway, "No weather data", err.Error(), nil)
	case errors.Is(err, weather.ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		writeProblem(w, r, http.StatusServiceUnavailable, "Weather provider rate limit", "retry later", nil)
	case errors.Is(err, weather.ErrUpstream):
		writeProblem(w, r, http.StatusBadGateway, "Weather provider unavailable", "", nil)
	case errors.Is(err, context.DeadlineExceeded):
		writeProblem(w, r, http.StatusGatewayTimeout, "Timeout", "the forecast took too long", nil)
	case errors.Is(err, context.Canceled):
		// The client went away; nobody reads the response.
		return
	default:
		s.cfg.Logger.ErrorContext(r.Context(), "forecast failed", "err", err, "request_id", requestIDFrom(r.Context()))
		writeProblem(w, r, http.StatusInternalServerError, "Internal error", "", nil)
	}
}

// queryParser collects every parse error so the client sees them all.
type queryParser struct {
	q    url.Values
	errs []string
}

func ptr[T any](v T) *T { return &v }

func (p *queryParser) float(name string, def *float64) float64 {
	v := p.q.Get(name)
	if v == "" {
		if def == nil {
			p.errs = append(p.errs, name+": required")
			return 0
		}
		return *def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		p.errs = append(p.errs, name+": "+strconv.Quote(v)+" is not a number")
	}
	return f
}

func (p *queryParser) int(name string, def int) int {
	v := p.q.Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.errs = append(p.errs, name+": "+strconv.Quote(v)+" is not an integer")
	}
	return n
}

func (p *queryParser) bool(name string) bool {
	v := p.q.Get(name)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.errs = append(p.errs, name+": "+strconv.Quote(v)+" is not a boolean")
	}
	return b
}
