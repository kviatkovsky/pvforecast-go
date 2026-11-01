package solarpos_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/kviatkovsky/pvforecast-go/internal/testutil"
	"github.com/kviatkovsky/pvforecast-go/solarpos"
)

// angleDiff returns a − b wrapped to (-180, 180].
func angleDiff(a, b float64) float64 {
	d := math.Mod(a-b, 360)
	switch {
	case d > 180:
		d -= 360
	case d <= -180:
		d += 360
	}
	return d
}

// fixtureCase is one row of internal/testdata/solarpos_spa.csv.
type fixtureCase struct {
	t     time.Time
	site  solarpos.Site
	dt    float64
	want  solarpos.Position
	where string
}

func loadFixture(tb testing.TB) []fixtureCase {
	tb.Helper()
	tbl := testutil.LoadCSV(tb, "solarpos_spa.csv")
	out := make([]fixtureCase, len(tbl.Rows))
	for i := range tbl.Rows {
		ts := tbl.String(tb, i, "time")
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			tb.Fatalf("row %d: %v", i, err)
		}
		c := fixtureCase{
			t: t,
			site: solarpos.Site{
				Lat:       tbl.Float(tb, i, "lat"),
				Lon:       tbl.Float(tb, i, "lon"),
				AltitudeM: tbl.Float(tb, i, "altitude_m"),
				Atmosphere: &solarpos.Atmosphere{
					PressurePa: tbl.Float(tb, i, "pressure_pa"),
					TempC:      tbl.Float(tb, i, "temp_c"),
				},
			},
			dt: tbl.Float(tb, i, "delta_t"),
			want: solarpos.Position{
				Zenith:            tbl.Float(tb, i, "zenith"),
				ApparentZenith:    tbl.Float(tb, i, "apparent_zenith"),
				Elevation:         tbl.Float(tb, i, "elevation"),
				ApparentElevation: tbl.Float(tb, i, "apparent_elevation"),
				Azimuth:           tbl.Float(tb, i, "azimuth"),
				EarthSunDistance:  tbl.Float(tb, i, "earth_sun_distance"),
			},
		}
		c.where = fmt.Sprintf("%s (%g, %g)", ts, c.site.Lat, c.site.Lon)
		out[i] = c
	}
	return out
}

// TestSPAMatchesPvlib compares SPA with pvlib.solarposition.spa_python over
// seven sites (including both polar regions and the antimeridian) and a
// year of samples plus dates from 1950 to 2100.
func TestSPAMatchesPvlib(t *testing.T) {
	t.Parallel()
	const angleTol = 1e-6 // degrees; the project target is 1e-3
	var (
		zen    = testutil.MaxErr{Name: "zenith", Tol: angleTol}
		appZen = testutil.MaxErr{Name: "apparent zenith", Tol: angleTol}
		az     = testutil.MaxErr{Name: "azimuth", Tol: angleTol}
		dist   = testutil.MaxErr{Name: "earth-sun distance", Tol: 1e-9}
	)
	for _, c := range loadFixture(t) {
		got, err := solarpos.SPA{DeltaT: c.dt}.Position(c.t, c.site)
		if err != nil {
			t.Fatalf("%s: %v", c.where, err)
		}
		zen.Observe(got.Zenith, c.want.Zenith, c.where)
		appZen.Observe(got.ApparentZenith, c.want.ApparentZenith, c.where)
		az.ObserveDiff(angleDiff(got.Azimuth, c.want.Azimuth), c.where)
		dist.Observe(got.EarthSunDistance, c.want.EarthSunDistance, c.where)

		if d := got.Elevation + got.Zenith - 90; math.Abs(d) > 1e-9 {
			t.Errorf("%s: elevation + zenith = 90%+g", c.where, d)
		}
	}
	for _, m := range []*testutil.MaxErr{&zen, &appZen, &az, &dist} {
		m.Report(t)
	}
}

// TestSPAReportExample checks the worked example of Reda & Andreas (2004),
// Table A5.1, which the report gives to five decimal places.
func TestSPAReportExample(t *testing.T) {
	t.Parallel()
	ts := time.Date(2003, time.October, 17, 12, 30, 30, 0, time.FixedZone("MST", -7*3600))
	site := solarpos.Site{
		Lat:        39.742476,
		Lon:        -105.1786,
		AltitudeM:  1830.14,
		Atmosphere: &solarpos.Atmosphere{PressurePa: 82000, TempC: 11},
	}
	got, err := solarpos.SPA{DeltaT: 67}.Position(ts, site)
	if err != nil {
		t.Fatal(err)
	}
	const tol = 5e-6
	if d := got.ApparentZenith - 50.11162; math.Abs(d) > tol {
		t.Errorf("apparent zenith = %.6f, want 50.11162", got.ApparentZenith)
	}
	if d := got.Azimuth - 194.34024; math.Abs(d) > tol {
		t.Errorf("azimuth = %.6f, want 194.34024", got.Azimuth)
	}
}

func TestSPADefaults(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, time.March, 20, 9, 15, 0, 0, time.UTC)
	site := solarpos.Site{Lat: 50.0647, Lon: 19.9450, AltitudeM: 219}

	tests := []struct {
		name      string
		alg       solarpos.SPA
		site      solarpos.Site
		reference solarpos.Site
		refAlg    solarpos.SPA
	}{
		{
			name:      "zero DeltaT selects DefaultDeltaT",
			alg:       solarpos.SPA{},
			site:      site,
			refAlg:    solarpos.SPA{DeltaT: solarpos.DefaultDeltaT},
			reference: site,
		},
		{
			name:   "nil Atmosphere uses standard pressure and DefaultTempC",
			alg:    solarpos.SPA{},
			site:   site,
			refAlg: solarpos.SPA{},
			reference: solarpos.Site{
				Lat: site.Lat, Lon: site.Lon, AltitudeM: site.AltitudeM,
				Atmosphere: &solarpos.Atmosphere{
					PressurePa: solarpos.StandardPressure(site.AltitudeM),
					TempC:      solarpos.DefaultTempC,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.alg.Position(ts, tt.site)
			if err != nil {
				t.Fatal(err)
			}
			want, err := tt.refAlg.Position(ts, tt.reference)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestStandardPressure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		altitude float64
		want     float64 // Pa, ICAO standard atmosphere table
		tol      float64
	}{
		{altitude: 0, want: 101325, tol: 1e-9},
		{altitude: 1000, want: 89874.6, tol: 1},
		{altitude: 11000, want: 22632.1, tol: 2}, // tropopause
		{altitude: 50000, want: 0, tol: 0},       // beyond the formula
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%gm", tt.altitude), func(t *testing.T) {
			t.Parallel()
			if got := solarpos.StandardPressure(tt.altitude); math.Abs(got-tt.want) > tt.tol {
				t.Errorf("StandardPressure(%g) = %.1f, want %.1f ± %g", tt.altitude, got, tt.want, tt.tol)
			}
		})
	}
	if p := solarpos.StandardPressure(-400); p <= 101325 {
		t.Errorf("StandardPressure(-400) = %.1f, want above sea-level pressure", p)
	}
}
