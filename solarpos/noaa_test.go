package solarpos_test

import (
	"math"
	"testing"

	"github.com/kviatkovsky/pvforecast-go/internal/testutil"
	"github.com/kviatkovsky/pvforecast-go/solarpos"
)

// TestNOAAAccuracy measures NOAA against the pvlib SPA reference and guards
// the accuracy documented in the README. Only daytime samples are compared:
// that is where the angles drive irradiance.
func TestNOAAAccuracy(t *testing.T) {
	t.Parallel()
	var (
		zen    = testutil.MaxErr{Name: "zenith", Tol: 0.02}
		appZen = testutil.MaxErr{Name: "apparent zenith", Tol: 0.02}
		sep    = testutil.MaxErr{Name: "angular separation", Tol: 0.02}
		az     = testutil.MaxErr{Name: "azimuth (zenith>10°)", Tol: 0.05}
		dist   = testutil.MaxErr{Name: "earth-sun distance", Tol: 1e-4}
	)
	for _, c := range loadFixture(t) {
		if c.want.Elevation <= 0 {
			continue
		}
		got, err := solarpos.NOAA{}.Position(c.t, c.site)
		if err != nil {
			t.Fatalf("%s: %v", c.where, err)
		}
		zen.Observe(got.Zenith, c.want.Zenith, c.where)
		// Close to the horizon the two refraction models differ by design.
		if c.want.Elevation > 10 {
			appZen.Observe(got.ApparentZenith, c.want.ApparentZenith, c.where)
		}
		sep.ObserveDiff(separation(got, c.want), c.where)
		// Azimuth is ill-conditioned near the zenith, where a tiny position
		// error swings it widely; the separation above covers that case.
		if c.want.Zenith > 10 {
			az.ObserveDiff(angleDiff(got.Azimuth, c.want.Azimuth), c.where)
		}
		dist.Observe(got.EarthSunDistance, c.want.EarthSunDistance, c.where)
	}
	for _, m := range []*testutil.MaxErr{&zen, &appZen, &sep, &az, &dist} {
		m.Report(t)
	}
}

// separation returns the great-circle angle, in degrees, between the
// geometric sun directions of two positions.
func separation(a, b solarpos.Position) float64 {
	const d2r = math.Pi / 180
	za, zb := a.Zenith*d2r, b.Zenith*d2r
	cosSep := math.Cos(za)*math.Cos(zb) +
		math.Sin(za)*math.Sin(zb)*math.Cos((a.Azimuth-b.Azimuth)*d2r)
	return math.Acos(math.Min(1, cosSep)) / d2r
}
