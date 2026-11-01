package solarpos_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/kviatkovsky/pvforecast-go/solarpos"
)

var algorithms = []struct {
	name string
	alg  solarpos.Algorithm
}{
	{"SPA", solarpos.SPA{}},
	{"NOAA", solarpos.NOAA{}},
}

var krakow = solarpos.Site{Lat: 50.0647, Lon: 19.9450, AltitudeM: 219}

func TestValidation(t *testing.T) {
	t.Parallel()
	valid := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	nan := math.NaN()
	tests := []struct {
		name    string
		t       time.Time
		site    solarpos.Site
		spa     solarpos.SPA // ΔT only matters for SPA
		wantErr error
	}{
		{"valid", valid, krakow, solarpos.SPA{}, nil},
		{"latitude above 90", valid, solarpos.Site{Lat: 90.1}, solarpos.SPA{}, solarpos.ErrInvalidSite},
		{"latitude NaN", valid, solarpos.Site{Lat: nan}, solarpos.SPA{}, solarpos.ErrInvalidSite},
		{"longitude below -180", valid, solarpos.Site{Lon: -180.5}, solarpos.SPA{}, solarpos.ErrInvalidSite},
		{"altitude infinite", valid, solarpos.Site{AltitudeM: math.Inf(1)}, solarpos.SPA{}, solarpos.ErrInvalidSite},
		{"altitude below SPA range", valid, solarpos.Site{AltitudeM: -7e6}, solarpos.SPA{}, solarpos.ErrInvalidSite},
		{"zero pressure", valid, solarpos.Site{Atmosphere: &solarpos.Atmosphere{TempC: 10}}, solarpos.SPA{}, solarpos.ErrInvalidSite},
		{"temperature at absolute zero", valid, solarpos.Site{Atmosphere: &solarpos.Atmosphere{PressurePa: 1e5, TempC: -273.15}}, solarpos.SPA{}, solarpos.ErrInvalidSite},
		{"temperature at SPA formula pole", valid, solarpos.Site{Atmosphere: &solarpos.Atmosphere{PressurePa: 1e5, TempC: -273}}, solarpos.SPA{}, solarpos.ErrInvalidSite},
		{"temperature NaN", valid, solarpos.Site{Atmosphere: &solarpos.Atmosphere{PressurePa: 1e5, TempC: nan}}, solarpos.SPA{}, solarpos.ErrInvalidSite},
		{"year after 6000", time.Date(6001, 1, 1, 0, 0, 0, 0, time.UTC), krakow, solarpos.SPA{}, solarpos.ErrOutOfRange},
		{"year before -2000", time.Date(-2001, 12, 31, 0, 0, 0, 0, time.UTC), krakow, solarpos.SPA{}, solarpos.ErrOutOfRange},
		{"zero time", time.Time{}, krakow, solarpos.SPA{}, nil}, // year 1 is inside the range
	}
	for _, tt := range tests {
		for _, a := range algorithms {
			t.Run(a.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				alg := a.alg
				if _, ok := alg.(solarpos.SPA); ok {
					alg = tt.spa
				}
				_, err := alg.Position(tt.t, tt.site)
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("err = %v, want %v", err, tt.wantErr)
				}
			})
		}
	}
}

func TestSPADeltaTRange(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	for _, dt := range []float64{-8000.5, 8000.5, math.NaN()} {
		if _, err := (solarpos.SPA{DeltaT: dt}).Position(ts, krakow); !errors.Is(err, solarpos.ErrOutOfRange) {
			t.Errorf("ΔT %g: err = %v, want ErrOutOfRange", dt, err)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	t.Parallel()
	tromso := solarpos.Site{Lat: 69.6492, Lon: 18.9553, AltitudeM: 10}
	tests := []struct {
		name  string
		t     time.Time
		site  solarpos.Site
		only  string // run for this algorithm only; empty runs all
		check func(t *testing.T, p solarpos.Position)
	}{
		{
			name: "polar day: midnight sun in Tromsø",
			t:    time.Date(2026, time.June, 21, 22, 45, 0, 0, time.UTC), // solar midnight
			site: tromso,
			check: func(t *testing.T, p solarpos.Position) {
				t.Helper()
				if p.ApparentElevation <= 0 {
					t.Errorf("apparent elevation = %.3f°, want above horizon", p.ApparentElevation)
				}
				if d := math.Abs(angleDiff(p.Azimuth, 0)); d > 2 {
					t.Errorf("azimuth = %.3f°, want within 2° of north", p.Azimuth)
				}
			},
		},
		{
			name: "polar night: no sun at noon in Tromsø",
			t:    time.Date(2026, time.December, 21, 10, 45, 0, 0, time.UTC),
			site: tromso,
			check: func(t *testing.T, p solarpos.Position) {
				t.Helper()
				if p.ApparentElevation >= 0 {
					t.Errorf("apparent elevation = %.3f°, want below horizon", p.ApparentElevation)
				}
			},
		},
		{
			// NOAA deliberately keeps a tiny correction below the horizon,
			// as in the NOAA Solar Calculator.
			name: "no refraction well below the horizon",
			t:    time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
			site: krakow,
			only: "SPA",
			check: func(t *testing.T, p solarpos.Position) {
				t.Helper()
				if p.ApparentZenith != p.Zenith {
					t.Errorf("apparent zenith %.6f != zenith %.6f", p.ApparentZenith, p.Zenith)
				}
			},
		},
		{
			name: "north pole at the June solstice",
			t:    time.Date(2026, time.June, 21, 8, 24, 0, 0, time.UTC),
			site: solarpos.Site{Lat: 90},
			check: func(t *testing.T, p solarpos.Position) {
				t.Helper()
				// Elevation equals the declination, about +23.44°.
				if math.Abs(p.Elevation-23.44) > 0.01 {
					t.Errorf("elevation = %.4f°, want ≈ 23.44°", p.Elevation)
				}
			},
		},
	}
	for _, tt := range tests {
		for _, a := range algorithms {
			if tt.only != "" && tt.only != a.name {
				continue
			}
			t.Run(a.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				p, err := a.alg.Position(tt.t, tt.site)
				if err != nil {
					t.Fatal(err)
				}
				checkRanges(t, p)
				tt.check(t, p)
			})
		}
	}
}

// TestInvariants checks properties that hold for any input: the result
// depends on the instant, not its time zone, and longitudes ±180° are the
// same meridian.
func TestInvariants(t *testing.T) {
	t.Parallel()
	utc := time.Date(2026, time.March, 29, 0, 30, 0, 0, time.UTC) // EU DST switch day
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	for _, a := range algorithms {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			p1, _ := a.alg.Position(utc, krakow)
			p2, _ := a.alg.Position(utc.In(warsaw), krakow)
			if p1 != p2 {
				t.Errorf("time zone changed the result: %+v vs %+v", p1, p2)
			}
			east, _ := a.alg.Position(utc, solarpos.Site{Lat: -17.7, Lon: 180})
			west, _ := a.alg.Position(utc, solarpos.Site{Lat: -17.7, Lon: -180})
			if d := math.Abs(east.Zenith - west.Zenith); d > 1e-9 {
				t.Errorf("zenith differs across the antimeridian by %g°", d)
			}
			if d := math.Abs(angleDiff(east.Azimuth, west.Azimuth)); d > 1e-9 {
				t.Errorf("azimuth differs across the antimeridian by %g°", d)
			}
		})
	}
}

// checkRanges asserts the invariants every Position must satisfy.
func checkRanges(tb testing.TB, p solarpos.Position) {
	tb.Helper()
	switch {
	case !(p.Zenith >= 0 && p.Zenith <= 180):
		tb.Errorf("zenith %g outside [0, 180]", p.Zenith)
	case !(p.Azimuth >= 0 && p.Azimuth < 360):
		tb.Errorf("azimuth %g outside [0, 360)", p.Azimuth)
	case !(p.ApparentZenith <= p.Zenith && p.Zenith-p.ApparentZenith < 2):
		tb.Errorf("refraction %g° outside [0, 2)", p.Zenith-p.ApparentZenith)
	case !(p.EarthSunDistance > 0.98 && p.EarthSunDistance < 1.02):
		tb.Errorf("earth-sun distance %g AU implausible", p.EarthSunDistance)
	case !(p.DNIExtra > 1300 && p.DNIExtra < 1420):
		tb.Errorf("DNIExtra %g W/m² implausible", p.DNIExtra)
	}
}
