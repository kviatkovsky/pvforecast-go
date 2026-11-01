package forecast

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/kviatkovsky/pvforecast-go/irradiance"
	"github.com/kviatkovsky/pvforecast-go/power"
	"github.com/kviatkovsky/pvforecast-go/solarpos"
	"github.com/kviatkovsky/pvforecast-go/thermal"
	"github.com/kviatkovsky/pvforecast-go/weather"
)

// MaxDays is the longest horizon Run accepts.
const MaxDays = 7

// dateLayout formats DailyEnergy.Date.
const dateLayout = "2006-01-02"

// ErrNoData reports that the weather provider returned no samples for the
// requested window.
var ErrNoData = errors.New("forecast: no weather data for the requested window")

// Options select models and conventions. The zero value uses the isotropic
// sky model, the SPA, UTC dates and wind as reported; DefaultOptions
// returns the recommended configuration.
type Options struct {
	// Transposition is the sky-diffuse model.
	Transposition irradiance.Model
	// SolarPos computes the sun position. Nil selects solarpos.SPA{}.
	SolarPos solarpos.Algorithm
	// Location is the time zone for daily totals. Nil selects UTC.
	Location *time.Location
	// ModuleHeightM, if positive, scales the 10 m forecast wind to this
	// height with the 1/7 power law before the thermal model (ADR 0005).
	ModuleHeightM float64
	// Instant makes Run request instantaneous irradiance instead of hourly
	// means.
	Instant bool
	// Now is the clock used by Run. Nil selects time.Now.
	Now func() time.Time
}

// ErrInvalidOptions is wrapped by errors about Options fields.
var ErrInvalidOptions = errors.New("forecast: invalid options")

func (o Options) validate() error {
	if !o.Transposition.Valid() {
		return fmt.Errorf("%w: %w: %d", ErrInvalidOptions, irradiance.ErrUnknownModel, int(o.Transposition))
	}
	if !(o.ModuleHeightM >= 0) {
		return fmt.Errorf("%w: module height %g m must be zero (off) or positive", ErrInvalidOptions, o.ModuleHeightM)
	}
	return nil
}

// DefaultOptions returns the recommended options: Perez transposition and
// the SPA.
func DefaultOptions() Options {
	return Options{Transposition: irradiance.Perez, SolarPos: solarpos.SPA{}}
}

// Point is the forecast for one weather sample.
type Point struct {
	// Time is the weather sample's timestamp; how it relates to the
	// averaging interval is given by Result.Label.
	Time time.Time
	// POA is the plane-of-array global irradiance in W/m², averaged over
	// arrays weighted by their kWp.
	POA float64
	// CellTempC is the cell temperature in °C, averaged like POA.
	CellTempC float64
	// DCkW is the DC power of all arrays after system losses.
	DCkW float64
	// ACkW is the inverter output.
	ACkW float64
	// Clipped reports that the inverter limited the output.
	Clipped bool
}

// DailyEnergy is the AC energy of one local calendar day.
type DailyEnergy struct {
	Date string  `json:"date"` // YYYY-MM-DD in Options.Location
	KWh  float64 `json:"kwh"`  // AC energy
}

// Result is a forecast time series with its energy totals.
type Result struct {
	Points   []Point
	Daily    []DailyEnergy // in date order
	TotalKWh float64
	PeakACkW float64
	// Interval and Label are copied from the weather series.
	Interval time.Duration
	Label    weather.Label
	Source   string
}

// All returns an iterator over the points, for range-over-func callers.
func (r Result) All() iter.Seq2[int, Point] {
	return func(yield func(int, Point) bool) {
		for i, p := range r.Points {
			if !yield(i, p) {
				return
			}
		}
	}
}

// Compute runs the physics pipeline over a weather series. It is pure and
// deterministic: no I/O, no clock, no global state.
//
// For each sample, solar geometry is evaluated at the instant that
// represents it: the midpoint of an averaging interval, or the timestamp of
// an instantaneous value (see weather.Label.RepresentativeOffset and
// ADR 0006). Energy is power × interval, attributed to the local date of
// that same instant.
//
// Per sample and array: solar position → transposition → IAM on the beam →
// cell temperature → PVWatts DC; then the DC of all arrays, reduced by the
// system losses, goes through one inverter.
func Compute(sys System, ws weather.Series, opt Options) (Result, error) {
	if err := sys.Validate(); err != nil {
		return Result{}, err
	}
	if err := ws.Validate(); err != nil {
		return Result{}, fmt.Errorf("forecast: %w", err)
	}
	if err := opt.validate(); err != nil {
		return Result{}, err
	}

	alg := opt.SolarPos
	if alg == nil {
		alg = solarpos.SPA{}
	}
	loc := opt.Location
	if loc == nil {
		loc = time.UTC
	}
	site := solarpos.Site{Lat: sys.Lat, Lon: sys.Lon, AltitudeM: sys.AltitudeM}
	arrays := sys.resolve()
	totalKWp := sys.DCkWp()
	lossFactor := sys.Losses.Factor()
	albedo := sys.albedo()
	offset := ws.Label.RepresentativeOffset(ws.Interval)
	hours := ws.Interval.Hours()

	res := Result{
		Points:   make([]Point, len(ws.Samples)),
		Daily:    make([]DailyEnergy, 0, len(ws.Samples)/24+2),
		Interval: ws.Interval,
		Label:    ws.Label,
		Source:   ws.Source,
	}
	var (
		lastY, lastD int
		lastM        time.Month
		dateBuf      [len(dateLayout)]byte
	)

	for i, smp := range ws.Samples {
		at := smp.Time.Add(offset)
		pos, err := alg.Position(at, site)
		if err != nil {
			return Result{}, fmt.Errorf("forecast: sample %d (%s): %w", i, smp.Time.Format(time.RFC3339), err)
		}
		wind := smp.WindMS
		if opt.ModuleHeightM > 0 {
			wind = thermal.WindAtHeight(wind, 10, opt.ModuleHeightM, thermal.OpenTerrainShear)
		}

		var pdcW, poaSum, tcellSum float64
		for _, a := range arrays {
			poa, err := irradiance.Transpose(opt.Transposition, irradiance.Input{
				TiltDeg: a.tilt, SurfaceAzimuthDeg: a.az,
				ZenithDeg: pos.ApparentZenith, SolarAzimuthDeg: pos.Azimuth,
				GHI: smp.GHI, DNI: smp.DNI, DHI: smp.DHI,
				DNIExtra: pos.DNIExtra, Albedo: albedo,
			})
			if err != nil {
				return Result{}, fmt.Errorf("forecast: sample %d: %w", i, err)
			}
			effective := poa.Beam*a.iam.Modifier(poa.AOIDeg) + poa.Sky.Total + poa.GroundDiffuse
			tcell := a.thermal.CellTemp(poa.Global, smp.AirTempC, wind)
			pdcW += power.PVWattsDC(effective, tcell, a.pdc0W, a.gamma)
			weight := a.pdc0W / 1000
			poaSum += poa.Global * weight
			tcellSum += tcell * weight
		}
		pdcW *= lossFactor
		pacW, clipped := sys.Inverter.AC(pdcW)

		p := Point{
			Time:      smp.Time,
			POA:       poaSum / totalKWp,
			CellTempC: tcellSum / totalKWp,
			DCkW:      pdcW / 1000,
			ACkW:      pacW / 1000,
			Clipped:   clipped,
		}
		res.Points[i] = p
		res.PeakACkW = max(res.PeakACkW, p.ACkW)

		kWh := p.ACkW * hours
		res.TotalKWh += kWh
		local := at.In(loc)
		y, m, d := local.Date()
		if len(res.Daily) == 0 || y != lastY || m != lastM || d != lastD {
			date := string(local.AppendFormat(dateBuf[:0], dateLayout))
			res.Daily = append(res.Daily, DailyEnergy{Date: date})
			lastY, lastM, lastD = y, m, d
		}
		res.Daily[len(res.Daily)-1].KWh += kWh
	}
	return res, nil
}

// Run fetches a forecast for the next days local calendar days, starting
// at the current hour of today in opt.Location, and computes it. Samples
// are kept when the instant they represent (see Compute) falls inside the
// window returned by Window.
func Run(ctx context.Context, p weather.Provider, sys System, days int, opt Options) (Result, error) {
	if days < 1 || days > MaxDays {
		return Result{}, fmt.Errorf("forecast: days = %d outside [1, %d]", days, MaxDays)
	}
	if err := sys.Validate(); err != nil {
		return Result{}, err
	}
	start, end := Window(nowOf(opt), opt.Location, days)
	// Providers filter by timestamp label, but an interval-end label lies up
	// to one interval after the period it describes. Ask for one extra hour
	// and keep the samples whose representative instant is in the window.
	series, err := p.Forecast(ctx, weather.Request{
		Lat: sys.Lat, Lon: sys.Lon, Start: start, End: end.Add(time.Hour), Instant: opt.Instant,
	})
	if err != nil {
		return Result{}, fmt.Errorf("forecast: fetch weather: %w", err)
	}
	series.Samples = clip(series, start, end)
	if len(series.Samples) == 0 {
		return Result{}, ErrNoData
	}
	return Compute(sys, series, opt)
}

// clip returns the samples whose representative instant is in [start, end).
func clip(s weather.Series, start, end time.Time) []weather.Sample {
	offset := s.Label.RepresentativeOffset(s.Interval)
	out := s.Samples[:0:0]
	for _, smp := range s.Samples {
		if at := smp.Time.Add(offset); !at.Before(start) && at.Before(end) {
			out = append(out, smp)
		}
	}
	return out
}

// Window returns the forecast window used by Run: from the start of the
// current hour to local midnight after the given number of calendar days
// in loc (nil means UTC). Today counts as the first day.
func Window(now time.Time, loc *time.Location, days int) (start, end time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	y, m, d := local.Date()
	end = time.Date(y, m, d+days, 0, 0, 0, 0, loc)
	return now.Truncate(time.Hour).UTC(), end.UTC()
}

func nowOf(opt Options) time.Time {
	if opt.Now != nil {
		return opt.Now()
	}
	return time.Now()
}
