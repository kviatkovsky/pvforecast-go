package irradiance_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/kviatkovsky/pvforecast-go/internal/testutil"
	"github.com/kviatkovsky/pvforecast-go/irradiance"
)

// Fixture values are stored with eight decimals; 1e-6 leaves room for
// rounding while being far tighter than any physical uncertainty.
const (
	irrTol   = 1e-6 // W/m²
	angleTol = 1e-6 // degrees
)

// TestTranspositionMatchesPvlib compares every model and component with
// pvlib over five sites, six tilts, five orientations, four cloud levels and
// three albedos, with dense sampling near the horizon.
func TestTranspositionMatchesPvlib(t *testing.T) {
	t.Parallel()
	tbl := testutil.LoadCSV(t, "irradiance_transposition.csv")

	type check struct {
		err  testutil.MaxErr
		got  func(irradiance.POA) float64
		col  string
		only irradiance.Model
	}
	checks := []*check{
		{err: testutil.MaxErr{Name: "AOI", Tol: angleTol}, col: "aoi", only: irradiance.Isotropic, got: func(p irradiance.POA) float64 { return p.AOIDeg }},
		{err: testutil.MaxErr{Name: "beam", Tol: irrTol}, col: "beam", only: irradiance.Isotropic, got: func(p irradiance.POA) float64 { return p.Beam }},
		{err: testutil.MaxErr{Name: "ground", Tol: irrTol}, col: "ground", only: irradiance.Isotropic, got: func(p irradiance.POA) float64 { return p.GroundDiffuse }},
		{err: testutil.MaxErr{Name: "isotropic", Tol: irrTol}, col: "isotropic", only: irradiance.Isotropic, got: func(p irradiance.POA) float64 { return p.Sky.Total }},
		{err: testutil.MaxErr{Name: "haydavies", Tol: irrTol}, col: "haydavies", only: irradiance.HayDavies, got: func(p irradiance.POA) float64 { return p.Sky.Total }},
		{err: testutil.MaxErr{Name: "haydavies iso", Tol: irrTol}, col: "haydavies_isotropic", only: irradiance.HayDavies, got: func(p irradiance.POA) float64 { return p.Sky.Isotropic }},
		{err: testutil.MaxErr{Name: "haydavies circ", Tol: irrTol}, col: "haydavies_circumsolar", only: irradiance.HayDavies, got: func(p irradiance.POA) float64 { return p.Sky.Circumsolar }},
		{err: testutil.MaxErr{Name: "perez", Tol: irrTol}, col: "perez", only: irradiance.Perez, got: func(p irradiance.POA) float64 { return p.Sky.Total }},
		{err: testutil.MaxErr{Name: "perez iso", Tol: irrTol}, col: "perez_isotropic", only: irradiance.Perez, got: func(p irradiance.POA) float64 { return p.Sky.Isotropic }},
		{err: testutil.MaxErr{Name: "perez circ", Tol: irrTol}, col: "perez_circumsolar", only: irradiance.Perez, got: func(p irradiance.POA) float64 { return p.Sky.Circumsolar }},
		{err: testutil.MaxErr{Name: "perez horizon", Tol: irrTol}, col: "perez_horizon", only: irradiance.Perez, got: func(p irradiance.POA) float64 { return p.Sky.Horizon }},
	}

	for i := range tbl.Rows {
		in := irradiance.Input{
			TiltDeg:           tbl.Float(t, i, "tilt"),
			SurfaceAzimuthDeg: tbl.Float(t, i, "surface_azimuth"),
			ZenithDeg:         tbl.Float(t, i, "zenith"),
			SolarAzimuthDeg:   tbl.Float(t, i, "sun_azimuth"),
			GHI:               tbl.Float(t, i, "ghi"),
			DNI:               tbl.Float(t, i, "dni"),
			DHI:               tbl.Float(t, i, "dhi"),
			DNIExtra:          tbl.Float(t, i, "dni_extra"),
			AirMass:           tbl.Float(t, i, "airmass"),
			Albedo:            tbl.Float(t, i, "albedo"),
		}
		where := fmt.Sprintf("row %d (tilt %g, az %g, zenith %.3f)", i+2, in.TiltDeg, in.SurfaceAzimuthDeg, in.ZenithDeg)
		var poa [3]irradiance.POA
		for _, m := range []irradiance.Model{irradiance.Isotropic, irradiance.HayDavies, irradiance.Perez} {
			p, err := irradiance.Transpose(m, in)
			if err != nil {
				t.Fatalf("%s: %v", where, err)
			}
			if d := p.Global - (p.Beam + p.Sky.Total + p.GroundDiffuse); math.Abs(d) > 1e-9 {
				t.Errorf("%s %s: Global is not the sum of its parts (%g)", where, m, d)
			}
			poa[m] = p
		}
		for _, c := range checks {
			c.err.Observe(c.got(poa[c.only]), tbl.Float(t, i, c.col), where)
		}
	}
	for _, c := range checks {
		c.err.Report(t)
	}
}

func TestRelativeAirMassMatchesPvlib(t *testing.T) {
	t.Parallel()
	tbl := testutil.LoadCSV(t, "irradiance_airmass.csv")
	m := testutil.MaxErr{Name: "air mass", Tol: 1e-7}
	for i := range tbl.Rows {
		z, want := tbl.Float(t, i, "zenith"), tbl.Float(t, i, "airmass")
		got := irradiance.RelativeAirMass(z)
		if math.IsNaN(want) {
			if !math.IsNaN(got) {
				t.Errorf("RelativeAirMass(%g) = %g, want NaN", z, got)
			}
			continue
		}
		m.Observe(got, want, fmt.Sprintf("zenith %g", z))
	}
	m.Report(t)
}

func TestErbsMatchesPvlib(t *testing.T) {
	t.Parallel()
	tbl := testutil.LoadCSV(t, "irradiance_erbs.csv")
	var (
		dni = testutil.MaxErr{Name: "erbs DNI", Tol: irrTol}
		dhi = testutil.MaxErr{Name: "erbs DHI", Tol: irrTol}
		kt  = testutil.MaxErr{Name: "erbs Kt", Tol: 1e-7}
	)
	for i := range tbl.Rows {
		ghi, z := tbl.Float(t, i, "ghi"), tbl.Float(t, i, "zenith")
		got := irradiance.Erbs(ghi, z, tbl.Float(t, i, "dni_extra"))
		where := fmt.Sprintf("GHI %g, zenith %g", ghi, z)
		dni.Observe(got.DNI, tbl.Float(t, i, "dni"), where)
		dhi.Observe(got.DHI, tbl.Float(t, i, "dhi"), where)
		kt.Observe(got.Kt, tbl.Float(t, i, "kt"), where)
	}
	for _, m := range []*testutil.MaxErr{&dni, &dhi, &kt} {
		m.Report(t)
	}
}
