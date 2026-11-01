package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/kviatkovsky/pvforecast-go/forecast"
	"github.com/kviatkovsky/pvforecast-go/irradiance"
	"github.com/kviatkovsky/pvforecast-go/power"
	"github.com/kviatkovsky/pvforecast-go/solarpos"
	"github.com/kviatkovsky/pvforecast-go/weather"
)

// Exit codes.
const (
	exitOK      = 0
	exitRuntime = 1
	exitUsage   = 2
)

// deps are the side effects run needs, injected so that tests are
// deterministic and offline.
type deps struct {
	provider weather.Provider
	now      func() time.Time
}

// usageError marks errors caused by the user's input (exit code 2).
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usagef(format string, args ...any) error { return usageError{fmt.Errorf(format, args...)} }

// options holds the parsed command line.
type options struct {
	lat, lon, altitude          float64
	kwp, tilt, azimuth          float64
	days                        int
	model, solarPos, format, tz string
	losses, dcac                float64
	config, logLevel            string
	instant, night              bool
	timeout                     time.Duration
	set                         map[string]bool // flags given explicitly
}

func newFlagSet(o *options, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("pvforecast", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Float64Var(&o.lat, "lat", 0, "site latitude in degrees, positive north (required without --config)")
	fs.Float64Var(&o.lon, "lon", 0, "site longitude in degrees, positive east (required without --config)")
	fs.Float64Var(&o.altitude, "altitude", 0, "site altitude above sea level in meters")
	fs.Float64Var(&o.kwp, "kwp", 0, "DC nameplate power in kWp (required without --config)")
	fs.Float64Var(&o.tilt, "tilt", 35, "module tilt in degrees, 0 = horizontal")
	fs.Float64Var(&o.azimuth, "azimuth", 180, "module azimuth in degrees clockwise from north, 180 = south")
	fs.IntVar(&o.days, "days", 2, fmt.Sprintf("forecast horizon in local calendar days, 1–%d, including today", forecast.MaxDays))
	fs.StringVar(&o.model, "model", "perez", "sky diffuse model: perez, haydavies or isotropic")
	fs.StringVar(&o.solarPos, "solarpos", "spa", "solar position algorithm: spa or noaa")
	fs.Float64Var(&o.losses, "losses", round(power.DefaultLosses.TotalPct(), 2),
		"total system losses in percent; if unset, the PVWatts loss categories are used")
	fs.Float64Var(&o.dcac, "dcac", 1.1, "DC/AC ratio: kWp divided by the inverter AC rating")
	fs.StringVar(&o.config, "config", "", "JSON system configuration file (multi-array systems)")
	fs.StringVar(&o.format, "format", "table", "output format: table, json or csv")
	fs.StringVar(&o.tz, "tz", "Local", "IANA time zone for display and daily totals, e.g. Europe/Warsaw")
	fs.BoolVar(&o.instant, "instant", false, "use instantaneous instead of hourly-mean irradiance")
	fs.BoolVar(&o.night, "night", false, "include night-time rows in table output")
	fs.DurationVar(&o.timeout, "timeout", 30*time.Second, "overall timeout for fetching the weather")
	fs.StringVar(&o.logLevel, "log-level", "warn", "log level: debug, info, warn or error")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage:\n  pvforecast [flags]\n  pvforecast version\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs
}

// run is the testable body of main. It returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) > 0 && args[0] == "version" {
		fmt.Fprintf(stdout, "pvforecast %s\n", buildVersion())
		return exitOK
	}

	var o options
	fs := newFlagSet(&o, stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "pvforecast: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return exitUsage
	}
	o.set = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { o.set[f.Name] = true })

	logger, err := newLogger(stderr, o.logLevel)
	if err != nil {
		fmt.Fprintf(stderr, "pvforecast: %v\n", err)
		return exitUsage
	}

	if err := execute(ctx, o, stdout, logger, d); err != nil {
		fmt.Fprintf(stderr, "pvforecast: %v\n", err)
		var ue usageError
		if errors.As(err, &ue) || errors.Is(err, forecast.ErrInvalidSystem) {
			return exitUsage
		}
		return exitRuntime
	}
	return exitOK
}

func execute(ctx context.Context, o options, stdout io.Writer, log *slog.Logger, d deps) error {
	sys, model, err := buildSystem(o)
	if err != nil {
		return err
	}
	if verr := sys.Validate(); verr != nil {
		return verr
	}
	opt, err := buildOptions(o, model, d.now)
	if err != nil {
		return err
	}
	render, err := renderer(o.format)
	if err != nil {
		return err
	}

	log.Info("forecast", "system", sys.Name, "kwp", sys.DCkWp(), "arrays", len(sys.Arrays),
		"days", o.days, "model", opt.Transposition, "tz", opt.Location)
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	start := time.Now()
	res, err := forecast.Run(ctx, d.provider, sys, o.days, opt)
	if err != nil {
		return err
	}
	log.Info("done", "points", len(res.Points), "total_kwh", res.TotalKWh, "elapsed", time.Since(start))
	return render(stdout, &report{sys: sys, opt: opt, res: res, night: o.night})
}

func buildSystem(o options) (forecast.System, irradiance.Model, error) {
	model, err := irradiance.ParseModel(o.model)
	if err != nil {
		return forecast.System{}, 0, usageError{err}
	}
	if o.config != "" {
		for _, name := range []string{"lat", "lon", "altitude", "kwp", "tilt", "azimuth", "losses"} {
			if o.set[name] {
				return forecast.System{}, 0, usagef("--%s cannot be combined with --config; set it in the file", name)
			}
		}
		cfg, err := loadConfig(o.config)
		if err != nil {
			return forecast.System{}, 0, usageError{err}
		}
		if cfg.Model != nil && !o.set["model"] {
			model = *cfg.Model
		}
		sys, err := cfg.system(o.dcac)
		if err != nil {
			return forecast.System{}, 0, usageError{err}
		}
		return sys, model, nil
	}

	for _, name := range []string{"lat", "lon", "kwp"} {
		if !o.set[name] {
			return forecast.System{}, 0, usagef("--%s is required (or use --config)", name)
		}
	}
	if !(o.dcac > 0) {
		return forecast.System{}, 0, usagef("--dcac %g must be positive", o.dcac)
	}
	sys := forecast.System{
		Name: "PV system", Lat: o.lat, Lon: o.lon, AltitudeM: o.altitude,
		Arrays:   []forecast.Array{{KWp: o.kwp, TiltDeg: o.tilt, AzimuthDeg: o.azimuth}},
		Inverter: power.Inverter{RatedACW: o.kwp * 1000 / o.dcac},
		Losses:   power.Losses{Other: o.losses},
	}
	if !o.set["losses"] {
		sys.Losses = power.DefaultLosses
	}
	return sys, model, nil
}

func buildOptions(o options, model irradiance.Model, now func() time.Time) (forecast.Options, error) {
	opt := forecast.Options{Transposition: model, Instant: o.instant, Now: now}
	switch strings.ToLower(o.solarPos) {
	case "spa":
		opt.SolarPos = solarpos.SPA{}
	case "noaa":
		opt.SolarPos = solarpos.NOAA{}
	default:
		return opt, usagef("--solarpos %q: want spa or noaa", o.solarPos)
	}
	loc, err := time.LoadLocation(o.tz)
	if err != nil {
		return opt, usagef("--tz %q: %w", o.tz, err)
	}
	opt.Location = loc
	if o.days < 1 || o.days > forecast.MaxDays {
		return opt, usagef("--days %d: want 1–%d", o.days, forecast.MaxDays)
	}
	return opt, nil
}

func newLogger(w io.Writer, level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("--log-level %q: want debug, info, warn or error", level)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l})), nil
}

// buildVersion prefers the version injected at link time and falls back to
// the module version recorded by "go install module@version".
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}
