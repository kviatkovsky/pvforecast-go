package irradiance_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/kviatkovsky/pvforecast-go/irradiance"
)

var models = []irradiance.Model{irradiance.Isotropic, irradiance.HayDavies, irradiance.Perez}

// clearNoon is a consistent clear-sky sample: GHI = DNI·cos z + DHI.
func clearNoon() irradiance.Input {
	const zenith = 35.0
	dni, dhi := 850.0, 110.0
	return irradiance.Input{
		TiltDeg: 35, SurfaceAzimuthDeg: 180,
		ZenithDeg: zenith, SolarAzimuthDeg: 180,
		DNI: dni, DHI: dhi, GHI: dni*math.Cos(zenith*math.Pi/180) + dhi,
		DNIExtra: 1361, Albedo: 0.25,
	}
}

func TestParseModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    irradiance.Model
		wantErr error
	}{
		{"isotropic", irradiance.Isotropic, nil},
		{"Perez", irradiance.Perez, nil},
		{" haydavies ", irradiance.HayDavies, nil},
		{"hay-davies", irradiance.HayDavies, nil},
		{"HAY_DAVIES", irradiance.HayDavies, nil},
		{"klucher", 0, irradiance.ErrUnknownModel},
		{"", 0, irradiance.ErrUnknownModel},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := irradiance.ParseModel(tt.in)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Errorf("ParseModel(%q) = %v, %v; want %v, %v", tt.in, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestModelJSON(t *testing.T) {
	t.Parallel()
	type config struct {
		Model irradiance.Model `json:"model"`
	}
	for _, m := range models {
		b, err := json.Marshal(config{Model: m})
		if err != nil {
			t.Fatalf("marshal %v: %v", m, err)
		}
		var back config
		if err := json.Unmarshal(b, &back); err != nil || back.Model != m {
			t.Errorf("round trip of %s gave %v, %v (json %s)", m, back.Model, err, b)
		}
	}
	if _, err := json.Marshal(config{Model: 7}); !errors.Is(err, irradiance.ErrUnknownModel) {
		t.Errorf("marshal of invalid model: err = %v, want ErrUnknownModel", err)
	}
	if err := json.Unmarshal([]byte(`{"model":"bogus"}`), new(config)); !errors.Is(err, irradiance.ErrUnknownModel) {
		t.Errorf("unmarshal of unknown name: err = %v, want ErrUnknownModel", err)
	}
	if s := irradiance.Model(-1).String(); s != "Model(-1)" {
		t.Errorf("String of invalid model = %q", s)
	}
}

func TestTransposeErrors(t *testing.T) {
	t.Parallel()
	in := clearNoon()
	if _, err := irradiance.Transpose(irradiance.Model(42), in); !errors.Is(err, irradiance.ErrUnknownModel) {
		t.Errorf("unknown model: err = %v", err)
	}
	for _, extra := range []float64{0, -1, math.NaN()} {
		in.DNIExtra = extra
		for _, m := range []irradiance.Model{irradiance.HayDavies, irradiance.Perez} {
			if _, err := irradiance.Transpose(m, in); !errors.Is(err, irradiance.ErrInvalidInput) {
				t.Errorf("%s with DNIExtra %g: err = %v, want ErrInvalidInput", m, extra, err)
			}
		}
		if _, err := irradiance.Transpose(irradiance.Isotropic, in); err != nil {
			t.Errorf("isotropic does not need DNIExtra, got %v", err)
		}
	}
}

// TestTransposeConventions pins the behavior near and below the horizon
// and for degenerate inputs (ADR 0004).
func TestTransposeConventions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		edit  func(in *irradiance.Input)
		check func(t *testing.T, m irradiance.Model, in irradiance.Input, p irradiance.POA)
	}{
		{
			name: "horizontal surface receives exactly GHI",
			edit: func(in *irradiance.Input) { in.TiltDeg = 0 },
			check: func(t *testing.T, m irradiance.Model, in irradiance.Input, p irradiance.POA) {
				t.Helper()
				if math.Abs(p.Global-in.GHI) > 1e-9 {
					t.Errorf("%s: Global = %.9f, want GHI %.9f", m, p.Global, in.GHI)
				}
			},
		},
		{
			name: "surface facing away from the sun gets no beam",
			edit: func(in *irradiance.Input) { in.TiltDeg, in.SurfaceAzimuthDeg = 90, 0 },
			check: func(t *testing.T, m irradiance.Model, _ irradiance.Input, p irradiance.POA) {
				t.Helper()
				if p.Beam != 0 || p.Sky.Circumsolar != 0 || p.AOIDeg <= 90 {
					t.Errorf("%s: beam %g, circumsolar %g, AOI %g", m, p.Beam, p.Sky.Circumsolar, p.AOIDeg)
				}
			},
		},
		{
			name: "sun below horizon: no beam, isotropic sky for every model",
			edit: func(in *irradiance.Input) {
				in.ZenithDeg, in.TiltDeg, in.SurfaceAzimuthDeg = 92, 90, 180
				in.DNI, in.DHI, in.GHI = 5, 10, 10
			},
			check: func(t *testing.T, m irradiance.Model, in irradiance.Input, p irradiance.POA) {
				t.Helper()
				want := irradiance.IsotropicSky(in.TiltDeg, in.DHI)
				if p.Beam != 0 || p.Sky != want {
					t.Errorf("%s: beam %g, sky %+v; want 0, %+v", m, p.Beam, p.Sky, want)
				}
			},
		},
		{
			name: "zero DHI gives zero sky diffuse, not NaN",
			edit: func(in *irradiance.Input) { in.DHI = 0 },
			check: func(t *testing.T, m irradiance.Model, _ irradiance.Input, p irradiance.POA) {
				t.Helper()
				if p.Sky != (irradiance.SkyDiffuse{}) {
					t.Errorf("%s: sky = %+v, want zero", m, p.Sky)
				}
			},
		},
		{
			name: "negative irradiance is clipped",
			edit: func(in *irradiance.Input) { in.DNI, in.DHI, in.GHI = -3, -1, -2 },
			check: func(t *testing.T, m irradiance.Model, _ irradiance.Input, p irradiance.POA) {
				t.Helper()
				if p.Global != 0 {
					t.Errorf("%s: Global = %g, want 0", m, p.Global)
				}
			},
		},
		{
			// Found by FuzzTranspose: DNI·cos θ is positive when both are
			// negative.
			name: "negative DNI with the sun behind the surface gives no beam",
			edit: func(in *irradiance.Input) { in.DNI, in.TiltDeg, in.SurfaceAzimuthDeg = -23, 90, 0 },
			check: func(t *testing.T, m irradiance.Model, _ irradiance.Input, p irradiance.POA) {
				t.Helper()
				if p.Beam != 0 {
					t.Errorf("%s: beam = %g, want 0", m, p.Beam)
				}
			},
		},
		{
			name: "sun on the horizon stays finite",
			edit: func(in *irradiance.Input) { in.ZenithDeg = 89.999; in.SolarAzimuthDeg = 180 },
			check: func(t *testing.T, m irradiance.Model, _ irradiance.Input, p irradiance.POA) {
				t.Helper()
				if math.IsNaN(p.Global) || math.IsInf(p.Global, 0) || p.Global < 0 {
					t.Errorf("%s: Global = %g", m, p.Global)
				}
			},
		},
		{
			name: "azimuth 0 and 360 are the same orientation",
			edit: func(in *irradiance.Input) { in.SurfaceAzimuthDeg = 360; in.SolarAzimuthDeg = 10; in.TiltDeg = 60 },
			check: func(t *testing.T, m irradiance.Model, in irradiance.Input, p irradiance.POA) {
				t.Helper()
				in.SurfaceAzimuthDeg = 0
				q, _ := irradiance.Transpose(m, in)
				if math.Abs(p.Global-q.Global) > 1e-9 {
					t.Errorf("%s: %g vs %g", m, p.Global, q.Global)
				}
			},
		},
		{
			name: "explicit air mass equals the computed default",
			edit: func(in *irradiance.Input) { in.AirMass = irradiance.RelativeAirMass(in.ZenithDeg) },
			check: func(t *testing.T, m irradiance.Model, in irradiance.Input, p irradiance.POA) {
				t.Helper()
				in.AirMass = 0
				q, _ := irradiance.Transpose(m, in)
				if p != q {
					t.Errorf("%s: %+v vs %+v", m, p, q)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, m := range models {
				in := clearNoon()
				tt.edit(&in)
				p, err := irradiance.Transpose(m, in)
				if err != nil {
					t.Fatalf("%s: %v", m, err)
				}
				tt.check(t, m, in, p)
			}
		})
	}
}

func TestErbsEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		ghi, zenith, extra float64
		want               irradiance.Decomposition
	}{
		{"night", 0, 120, 1361, irradiance.Decomposition{}},
		{"negative GHI is treated as zero", -4, 40, 1361, irradiance.Decomposition{}},
		{"beyond max zenith all diffuse", 20, 88, 1361, irradiance.Decomposition{DHI: 20, Kt: 20 / (1361 * irradiance.ErbsMinCosZenith)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := irradiance.Erbs(tt.ghi, tt.zenith, tt.extra)
			if math.Abs(got.DNI-tt.want.DNI) > 1e-12 || math.Abs(got.DHI-tt.want.DHI) > 1e-12 || math.Abs(got.Kt-tt.want.Kt) > 1e-12 {
				t.Errorf("Erbs(%g, %g) = %+v, want %+v", tt.ghi, tt.zenith, got, tt.want)
			}
		})
	}
	// Recomposition: DNI·cos z + DHI must give back GHI when the sun is up.
	for _, ghi := range []float64{10, 200, 600, 1000} {
		d := irradiance.Erbs(ghi, 30, 1361)
		if back := d.DNI*math.Cos(30*math.Pi/180) + d.DHI; math.Abs(back-ghi) > 1e-9 {
			t.Errorf("GHI %g recomposes to %g", ghi, back)
		}
	}
}

func TestAOI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                        string
		tilt, surfAz, zenith, sunAz float64
		want                        float64
	}{
		{"horizontal surface: AOI equals zenith", 0, 180, 37, 123, 37},
		{"sun on the normal", 30, 180, 30, 180, 0},
		{"vertical south wall, sun due east on horizon", 90, 180, 90, 90, 90},
		{"sun directly behind", 90, 180, 90, 0, 180},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := irradiance.AOI(tt.tilt, tt.surfAz, tt.zenith, tt.sunAz); math.Abs(got-tt.want) > 1e-6 {
				t.Errorf("AOI = %.9f, want %g", got, tt.want)
			}
		})
	}
}

// TestPerezNegativeTotal covers a vertical surface facing away from a low
// sun under a bright sky: the negative horizon term outweighs the isotropic
// part, and the model reports zero rather than negative irradiance.
func TestPerezNegativeTotal(t *testing.T) {
	t.Parallel()
	const cosTheta = -0.5 // sun behind the surface
	got := irradiance.PerezSky(90, 300, 1000, 1361, 84, 15, cosTheta)
	if got != (irradiance.SkyDiffuse{}) {
		t.Errorf("PerezSky = %+v, want all components zero", got)
	}
}
