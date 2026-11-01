package forecast_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kviatkovsky/pvforecast-go/forecast"
	"github.com/kviatkovsky/pvforecast-go/internal/testutil"
	"github.com/kviatkovsky/pvforecast-go/irradiance"
	"github.com/kviatkovsky/pvforecast-go/power"
	"github.com/kviatkovsky/pvforecast-go/thermal"
	"github.com/kviatkovsky/pvforecast-go/weather"
	"github.com/kviatkovsky/pvforecast-go/weather/openmeteo"
)

var fixtureStart = time.Date(2026, time.June, 20, 0, 0, 0, 0, time.UTC)

func southSystem() forecast.System {
	return forecast.System{
		Name: "Kraków south", Lat: 50.0647, Lon: 19.9450, AltitudeM: 219,
		Arrays:   []forecast.Array{{KWp: 10, TiltDeg: 35, AzimuthDeg: 180}},
		Inverter: power.Inverter{RatedACW: 10000 / 1.1},
		Losses:   power.DefaultLosses,
	}
}

func eastWestSystem() forecast.System {
	s := southSystem()
	s.Name = "Kraków east-west"
	s.Arrays = []forecast.Array{
		{Name: "east", KWp: 6, TiltDeg: 15, AzimuthDeg: 90},
		{Name: "west", KWp: 6, TiltDeg: 15, AzimuthDeg: 270},
	}
	s.Inverter = power.Inverter{RatedACW: 9000}
	return s
}

// fixtureProvider serves the synthetic Open-Meteo fixture through the real
// client, with an in-memory transport.
func fixtureProvider(tb testing.TB, name string) *openmeteo.Client {
	tb.Helper()
	body, err := os.ReadFile(testutil.TestdataPath("openmeteo/" + name))
	if err != nil {
		tb.Fatal(err)
	}
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	return openmeteo.New(
		openmeteo.WithHTTPClient(&http.Client{Transport: rt}),
		openmeteo.WithClock(func() time.Time { return fixtureStart }),
	)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fixtureSeries returns the synthetic hourly-mean series (interval-end
// labels) for 2026-06-20 to 2026-06-22.
func fixtureSeries(tb testing.TB) weather.Series {
	tb.Helper()
	s, err := fixtureProvider(tb, "forecast_averaged.json").Forecast(tb.Context(), weather.Request{
		Lat: 50.0647, Lon: 19.945, Start: fixtureStart, End: fixtureStart.Add(72 * time.Hour),
	})
	if err != nil {
		tb.Fatal(err)
	}
	return s
}

// TestComputeMatchesPvlibPipeline compares every point of two systems with
// the same chain assembled from pvlib functions (tools/fixtures/gen.py).
func TestComputeMatchesPvlibPipeline(t *testing.T) {
	t.Parallel()
	series := fixtureSeries(t)
	tbl := testutil.LoadCSV(t, "forecast_pipeline.csv")

	tests := []struct {
		name  string
		sys   forecast.System
		model irradiance.Model
	}{
		{"south", southSystem(), irradiance.Perez},
		{"eastwest", eastWestSystem(), irradiance.HayDavies},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := forecast.Compute(tt.sys, series, forecast.Options{Transposition: tt.model})
			if err != nil {
				t.Fatal(err)
			}
			byTime := map[string]forecast.Point{}
			for _, p := range res.Points {
				byTime[p.Time.Format(time.RFC3339)] = p
			}
			var (
				ac   = testutil.MaxErr{Name: tt.name + " AC", Tol: 1e-6}
				dc   = testutil.MaxErr{Name: tt.name + " DC", Tol: 1e-6}
				poa  = testutil.MaxErr{Name: tt.name + " POA", Tol: 1e-4}
				cell = testutil.MaxErr{Name: tt.name + " cell temp", Tol: 1e-6}
			)
			var rows int
			for i := range tbl.Rows {
				if tbl.String(t, i, "system") != tt.name {
					continue
				}
				rows++
				ts := tbl.String(t, i, "time")
				p, ok := byTime[ts]
				if !ok {
					t.Fatalf("no point at %s", ts)
				}
				ac.Observe(p.ACkW, tbl.Float(t, i, "ac_kw"), ts)
				dc.Observe(p.DCkW, tbl.Float(t, i, "dc_kw"), ts)
				poa.Observe(p.POA, tbl.Float(t, i, "poa"), ts)
				cell.Observe(p.CellTempC, tbl.Float(t, i, "cell_temp"), ts)
			}
			if rows != len(res.Points) {
				t.Errorf("fixture has %d rows, result %d points", rows, len(res.Points))
			}
			for _, m := range []*testutil.MaxErr{&ac, &dc, &poa, &cell} {
				m.Report(t)
			}
		})
	}
}

func TestComputeEnergyTotals(t *testing.T) {
	t.Parallel()
	warsaw := mustLoad(t, "Europe/Warsaw")
	res, err := forecast.Compute(southSystem(), fixtureSeries(t),
		forecast.Options{Transposition: irradiance.Perez, Location: warsaw})
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, d := range res.Daily {
		sum += d.KWh
		// Clear-sky June days in Kraków yield roughly 6–8 kWh per kWp.
		if d.Date != "2026-06-20" && d.Date != "2026-06-23" && (d.KWh < 60 || d.KWh > 85) {
			t.Errorf("%s: %.1f kWh is implausible for a clear June day on 10 kWp", d.Date, d.KWh)
		}
	}
	if math.Abs(sum-res.TotalKWh) > 1e-9 {
		t.Errorf("daily sum %.6f != total %.6f", sum, res.TotalKWh)
	}
	// Samples from 2026-06-20 00:00 UTC (02:00 CEST) through 06-22 20:00 UTC
	// (22:00 CEST): three local dates, no energy past midnight.
	if got := dates(res); got != "2026-06-20,2026-06-21,2026-06-22" {
		t.Errorf("dates = %s", got)
	}
	for _, p := range res.Points {
		if p.ACkW < 0 || p.ACkW > 10/1.1+1e-12 {
			t.Fatalf("AC %g kW outside [0, rating] at %s", p.ACkW, p.Time)
		}
		if h := p.Time.Hour(); (h <= 1 || h == 23) && p.ACkW != 0 {
			t.Errorf("AC %g kW at night (%s)", p.ACkW, p.Time)
		}
	}
	if res.PeakACkW <= 0 || res.Interval != time.Hour || res.Label != weather.IntervalEnd || res.Source != "open-meteo" {
		t.Errorf("metadata: peak %g, interval %v, label %v, source %q", res.PeakACkW, res.Interval, res.Label, res.Source)
	}
	var n int
	for i, p := range res.All() {
		if p != res.Points[i] {
			t.Fatal("All() yields a different point")
		}
		n++
		if n == 3 {
			break // exercise early termination
		}
	}
}

func dates(r forecast.Result) string {
	ds := make([]string, len(r.Daily))
	for i, d := range r.Daily {
		ds[i] = d.Date
	}
	return strings.Join(ds, ",")
}

// TestIntervalConvention pins ADR 0006: an hourly mean labeled at the end
// of its interval is modeled at the midpoint, so it must give the same
// result as the same values labeled instantaneous at the midpoint, or
// labeled at the start of the interval.
func TestIntervalConvention(t *testing.T) {
	t.Parallel()
	end := fixtureSeries(t)
	shift := func(label weather.Label, d time.Duration) weather.Series {
		s := end
		s.Label = label
		s.Samples = make([]weather.Sample, len(end.Samples))
		for i, smp := range end.Samples {
			smp.Time = smp.Time.Add(d)
			s.Samples[i] = smp
		}
		return s
	}
	ref := mustCompute(t, southSystem(), end)
	for _, tt := range []struct {
		name string
		s    weather.Series
	}{
		{"instant at the midpoint", shift(weather.Instant, -30*time.Minute)},
		{"interval-start label", shift(weather.IntervalStart, -time.Hour)},
	} {
		got := mustCompute(t, southSystem(), tt.s)
		for i := range ref.Points {
			if math.Abs(got.Points[i].ACkW-ref.Points[i].ACkW) > 1e-12 {
				t.Fatalf("%s: point %d AC %g != %g", tt.name, i, got.Points[i].ACkW, ref.Points[i].ACkW)
			}
		}
	}
	// Ignoring the convention (treating means as instantaneous at the label)
	// shifts the sun by 30 minutes. Over a symmetric clear day the daily
	// totals almost cancel out, but individual morning and evening hours
	// must change substantially.
	naive := mustCompute(t, southSystem(), shift(weather.Instant, 0))
	var worst float64
	for i, p := range ref.Points {
		if p.ACkW > 0.5 {
			worst = max(worst, math.Abs(naive.Points[i].ACkW-p.ACkW)/p.ACkW)
		}
	}
	if worst < 0.10 {
		t.Errorf("naive labeling changed hourly AC by at most %.1f %%, want > 10 %%", 100*worst)
	}
}

func mustCompute(tb testing.TB, sys forecast.System, s weather.Series) forecast.Result {
	tb.Helper()
	r, err := forecast.Compute(sys, s, forecast.DefaultOptions())
	if err != nil {
		tb.Fatal(err)
	}
	return r
}

func TestDailyTotalsAcrossDST(t *testing.T) {
	t.Parallel()
	warsaw := mustLoad(t, "Europe/Warsaw")
	// Two days of hourly interval-end samples around the spring-forward
	// switch on 2026-03-29 (CET → CEST at 01:00 UTC).
	start := time.Date(2026, time.March, 28, 0, 0, 0, 0, time.UTC)
	var s weather.Series
	s.Interval, s.Label = time.Hour, weather.IntervalEnd
	for h := 1; h <= 72; h++ {
		s.Samples = append(s.Samples, weather.Sample{
			Time: start.Add(time.Duration(h) * time.Hour), GHI: 400, DNI: 500, DHI: 150, AirTempC: 10, WindMS: 2,
		})
	}
	res, err := forecast.Compute(southSystem(), s, forecast.Options{Location: warsaw})
	if err != nil {
		t.Fatal(err)
	}
	// Count the samples that land on each local date, from the midpoint of
	// each interval.
	perDate := map[string]int{}
	for _, smp := range s.Samples {
		perDate[smp.Time.Add(-30*time.Minute).In(warsaw).Format("2006-01-02")]++
	}
	if perDate["2026-03-29"] != 23 {
		t.Fatalf("test setup: 2026-03-29 should have 23 local hours, got %d", perDate["2026-03-29"])
	}
	if got := dates(res); got != "2026-03-28,2026-03-29,2026-03-30,2026-03-31" {
		t.Fatalf("dates = %s", got)
	}
	// With the same irradiance every hour, energy is not constant (the sun
	// moves), so compare against the points instead: each date's energy is
	// the sum of its points.
	want := map[string]float64{}
	for i, p := range res.Points {
		want[s.Samples[i].Time.Add(-30*time.Minute).In(warsaw).Format("2006-01-02")] += p.ACkW
	}
	for _, d := range res.Daily {
		if math.Abs(d.KWh-want[d.Date]) > 1e-9 {
			t.Errorf("%s: %.6f kWh, want %.6f", d.Date, d.KWh, want[d.Date])
		}
	}
}

func TestMultiArrayEquivalence(t *testing.T) {
	t.Parallel()
	series := fixtureSeries(t)
	one := southSystem()
	two := southSystem()
	two.Arrays = []forecast.Array{
		{KWp: 4, TiltDeg: 35, AzimuthDeg: 180},
		{KWp: 6, TiltDeg: 35, AzimuthDeg: 180},
	}
	a, b := mustCompute(t, one, series), mustCompute(t, two, series)
	for i := range a.Points {
		if d := math.Abs(a.Points[i].ACkW - b.Points[i].ACkW); d > 1e-9 {
			t.Fatalf("point %d: split arrays differ by %g kW", i, d)
		}
	}
}

func TestOptionsEffects(t *testing.T) {
	t.Parallel()
	series := fixtureSeries(t)
	base := mustCompute(t, southSystem(), series)

	lowWind := forecast.DefaultOptions()
	lowWind.ModuleHeightM = 2 // less wind at module height → hotter cells
	hot, err := forecast.Compute(southSystem(), series, lowWind)
	if err != nil {
		t.Fatal(err)
	}
	if !(hot.TotalKWh < base.TotalKWh) {
		t.Errorf("module-height wind should lower energy: %.3f vs %.3f", hot.TotalKWh, base.TotalKWh)
	}

	sys := southSystem()
	sys.Arrays[0].Thermal = thermal.DefaultFaiman
	sys.Arrays[0].IAM = power.NoIAM{}
	sys.Arrays[0].GammaPdc = -0.003
	sys.Albedo = 0.6
	alt, err := forecast.Compute(sys, series, forecast.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if alt.TotalKWh == base.TotalKWh {
		t.Error("array options had no effect")
	}

	for _, bad := range []forecast.Options{
		{Transposition: 9},
		{ModuleHeightM: -1},
		{ModuleHeightM: math.NaN()},
	} {
		if _, err := forecast.Compute(southSystem(), series, bad); !errors.Is(err, forecast.ErrInvalidOptions) {
			t.Errorf("options %+v: err = %v, want ErrInvalidOptions", bad, err)
		}
	}
	if _, err := forecast.Compute(southSystem(), series, forecast.Options{Transposition: 9}); !errors.Is(err, irradiance.ErrUnknownModel) {
		t.Error("invalid model error does not wrap irradiance.ErrUnknownModel")
	}
	bad := series
	bad.Interval = 0
	if _, err := forecast.Compute(southSystem(), bad, forecast.Options{}); !errors.Is(err, weather.ErrInvalidSeries) {
		t.Errorf("invalid series: err = %v", err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		edit   func(*forecast.System)
		fields []string
	}{
		{"valid", func(*forecast.System) {}, nil},
		{"latitude", func(s *forecast.System) { s.Lat = 95 }, []string{"lat"}},
		{"longitude NaN", func(s *forecast.System) { s.Lon = math.NaN() }, []string{"lon"}},
		{"altitude", func(s *forecast.System) { s.AltitudeM = 12000 }, []string{"altitude_m"}},
		{"albedo", func(s *forecast.System) { s.Albedo = 1.5 }, []string{"albedo"}},
		{"no arrays", func(s *forecast.System) { s.Arrays = nil }, []string{"arrays"}},
		{"array fields", func(s *forecast.System) {
			s.Arrays[0] = forecast.Array{KWp: -1, TiltDeg: 91, AzimuthDeg: 400, GammaPdc: 0.01, Thermal: thermal.Faiman{}}
		}, []string{"arrays[0].kwp", "arrays[0].tilt", "arrays[0].azimuth", "arrays[0].gamma_pdc", "arrays[0].thermal"}},
		{"inverter", func(s *forecast.System) { s.Inverter = power.Inverter{} }, []string{"inverter"}},
		{"losses", func(s *forecast.System) { s.Losses.Soiling = 120 }, []string{"losses"}},
		{"tilt and azimuth edges are valid", func(s *forecast.System) { s.Arrays[0].TiltDeg, s.Arrays[0].AzimuthDeg = 90, 360 }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sys := southSystem()
			tt.edit(&sys)
			err := sys.Validate()
			if len(tt.fields) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, forecast.ErrInvalidSystem) {
				t.Fatalf("err = %v, want ErrInvalidSystem", err)
			}
			for _, f := range tt.fields {
				if !strings.Contains(err.Error(), f+":") {
					t.Errorf("error does not mention %s: %v", f, err)
				}
			}
			var fe *forecast.FieldError
			if !errors.As(err, &fe) {
				t.Errorf("error is not a *FieldError: %v", err)
			}
		})
	}
	if _, err := forecast.Compute(forecast.System{}, weather.Series{Interval: time.Hour}, forecast.Options{}); !errors.Is(err, forecast.ErrInvalidSystem) {
		t.Errorf("Compute with invalid system: err = %v", err)
	}
}

func TestWindow(t *testing.T) {
	t.Parallel()
	warsaw := mustLoad(t, "Europe/Warsaw")
	now := time.Date(2026, time.June, 20, 21, 47, 0, 0, time.UTC) // 23:47 CEST
	start, end := forecast.Window(now, warsaw, 2)
	if want := time.Date(2026, time.June, 20, 21, 0, 0, 0, time.UTC); !start.Equal(want) {
		t.Errorf("start = %v, want %v", start, want)
	}
	if want := time.Date(2026, time.June, 21, 22, 0, 0, 0, time.UTC); !end.Equal(want) { // 22nd 00:00 CEST
		t.Errorf("end = %v, want %v", end, want)
	}
	_, endUTC := forecast.Window(now, nil, 1)
	if want := time.Date(2026, time.June, 21, 0, 0, 0, 0, time.UTC); !endUTC.Equal(want) {
		t.Errorf("UTC end = %v, want %v", endUTC, want)
	}
}

type providerFunc func(context.Context, weather.Request) (weather.Series, error)

func (f providerFunc) Forecast(ctx context.Context, r weather.Request) (weather.Series, error) {
	return f(ctx, r)
}

func TestRun(t *testing.T) {
	t.Parallel()
	opt := forecast.DefaultOptions()
	opt.Now = func() time.Time { return fixtureStart }

	t.Run("end to end with the Open-Meteo client", func(t *testing.T) {
		t.Parallel()
		res, err := forecast.Run(t.Context(), fixtureProvider(t, "forecast_averaged.json"), southSystem(), 2, opt)
		if err != nil {
			t.Fatal(err)
		}
		// Hourly means labeled 01:00 on the first day to 00:00 after the last
		// one: exactly the 48 hours of the two UTC days.
		if len(res.Points) != 48 || dates(res) != "2026-06-20,2026-06-21" {
			t.Errorf("got %d points over %s, want 48 over two days", len(res.Points), dates(res))
		}
		if first := res.Points[0].Time; !first.Equal(fixtureStart.Add(time.Hour)) {
			t.Errorf("first point labeled %v, want 01:00", first)
		}
	})
	t.Run("passes the window and instant flag to the provider", func(t *testing.T) {
		t.Parallel()
		o := opt
		o.Instant = true
		var got weather.Request
		_, err := forecast.Run(t.Context(), providerFunc(func(_ context.Context, r weather.Request) (weather.Series, error) {
			got = r
			return weather.Series{}, nil
		}), southSystem(), 3, o)
		if !errors.Is(err, forecast.ErrNoData) {
			t.Errorf("empty series: err = %v, want ErrNoData", err)
		}
		// One extra hour covers the interval that ends at the window end.
		if !got.Instant || !got.Start.Equal(fixtureStart) || !got.End.Equal(fixtureStart.Add(73*time.Hour)) {
			t.Errorf("request = %+v", got)
		}
	})
	t.Run("wraps provider errors", func(t *testing.T) {
		t.Parallel()
		_, err := forecast.Run(t.Context(), providerFunc(func(context.Context, weather.Request) (weather.Series, error) {
			return weather.Series{}, weather.ErrRateLimited
		}), southSystem(), 1, opt)
		if !errors.Is(err, weather.ErrRateLimited) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("rejects bad input before any I/O", func(t *testing.T) {
		t.Parallel()
		never := providerFunc(func(context.Context, weather.Request) (weather.Series, error) {
			t.Error("provider called")
			return weather.Series{}, nil
		})
		for _, days := range []int{0, 8} {
			if _, err := forecast.Run(t.Context(), never, southSystem(), days, opt); err == nil {
				t.Errorf("days = %d accepted", days)
			}
		}
		if _, err := forecast.Run(t.Context(), never, forecast.System{}, 1, opt); !errors.Is(err, forecast.ErrInvalidSystem) {
			t.Errorf("invalid system: err = %v", err)
		}
	})
}

func mustLoad(tb testing.TB, name string) *time.Location {
	tb.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		tb.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}

func ExampleCompute() {
	sys := forecast.System{
		Lat: 50.0647, Lon: 19.9450, AltitudeM: 219,
		Arrays:   []forecast.Array{{KWp: 10, TiltDeg: 35, AzimuthDeg: 180}},
		Inverter: power.Inverter{RatedACW: 9091},
		Losses:   power.DefaultLosses,
	}
	// Two hourly means, labeled at the end of each hour (the Open-Meteo
	// convention).
	t := time.Date(2026, time.June, 21, 11, 0, 0, 0, time.UTC)
	series := weather.Series{
		Interval: time.Hour,
		Label:    weather.IntervalEnd,
		Samples: []weather.Sample{
			{Time: t, GHI: 880, DNI: 820, DHI: 130, AirTempC: 24, WindMS: 3},
			{Time: t.Add(time.Hour), GHI: 420, DNI: 150, DHI: 290, AirTempC: 25, WindMS: 4},
		},
	}
	res, err := forecast.Compute(sys, series, forecast.DefaultOptions())
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, p := range res.Points {
		fmt.Printf("%s  POA %4.0f W/m²  cell %2.0f °C  AC %.2f kW\n",
			p.Time.Format("15:04"), p.POA, p.CellTempC, p.ACkW)
	}
	fmt.Printf("total %.2f kWh\n", res.TotalKWh)
	// Output:
	// 11:00  POA  971 W/m²  cell 49 °C  AC 7.25 kW
	// 12:00  POA  442 W/m²  cell 36 °C  AC 3.49 kW
	// total 10.75 kWh
}
