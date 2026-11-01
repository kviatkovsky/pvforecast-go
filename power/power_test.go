package power_test

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/kviatkovsky/pvforecast-go/internal/testutil"
	"github.com/kviatkovsky/pvforecast-go/power"
)

func TestPVWattsDCMatchesPvlib(t *testing.T) {
	t.Parallel()
	tbl := testutil.LoadCSV(t, "power_dc.csv")
	m := testutil.MaxErr{Name: "PVWatts DC", Tol: 1e-6}
	for i := range tbl.Rows {
		g, tc := tbl.Float(t, i, "geff"), tbl.Float(t, i, "temp_cell")
		got := power.PVWattsDC(g, tc, tbl.Float(t, i, "pdc0"), tbl.Float(t, i, "gamma"))
		m.Observe(got, tbl.Float(t, i, "pdc"), fmt.Sprintf("G=%g T=%g", g, tc))
	}
	m.Report(t)
}

func TestInverterMatchesPvlib(t *testing.T) {
	t.Parallel()
	tbl := testutil.LoadCSV(t, "power_inverter.csv")
	m := testutil.MaxErr{Name: "PVWatts inverter", Tol: 1e-6}
	inv := power.Inverter{RatedACW: 10000} // Pdc0 = 10000/0.96 as in the fixture
	var clippedRows int
	for i := range tbl.Rows {
		pdc := tbl.Float(t, i, "pdc")
		got, clipped := inv.AC(pdc)
		want := tbl.Float(t, i, "pac")
		m.Observe(got, want, fmt.Sprintf("Pdc=%.1f", pdc))
		if clipped {
			clippedRows++
			if got != inv.RatedACW {
				t.Errorf("clipped output %g != rating", got)
			}
		}
	}
	m.Report(t)
	if clippedRows == 0 {
		t.Error("fixture never reaches clipping")
	}
}

func TestIAMMatchesPvlib(t *testing.T) {
	t.Parallel()
	tbl := testutil.LoadCSV(t, "power_iam.csv")
	phys := testutil.MaxErr{Name: "IAM physical", Tol: 1e-7}
	ash := testutil.MaxErr{Name: "IAM ASHRAE", Tol: 1e-7}
	for i := range tbl.Rows {
		aoi := tbl.Float(t, i, "aoi")
		where := fmt.Sprintf("AOI %g", aoi)
		phys.Observe(power.DefaultPhysicalIAM.Modifier(aoi), tbl.Float(t, i, "physical"), where)
		ash.Observe(power.DefaultASHRAEIAM.Modifier(aoi), tbl.Float(t, i, "ashrae"), where)
	}
	phys.Report(t)
	ash.Report(t)
}

func TestLossesMatchPvlib(t *testing.T) {
	t.Parallel()
	tbl := testutil.LoadCSV(t, "power_losses.csv")
	for i := range tbl.Rows {
		l := power.Losses{
			Soiling: tbl.Float(t, i, "soiling"), Shading: tbl.Float(t, i, "shading"),
			Snow: tbl.Float(t, i, "snow"), Mismatch: tbl.Float(t, i, "mismatch"),
			Wiring: tbl.Float(t, i, "wiring"), Connections: tbl.Float(t, i, "connections"),
			LID: tbl.Float(t, i, "lid"), Nameplate: tbl.Float(t, i, "nameplate"),
			Age: tbl.Float(t, i, "age"), Availability: tbl.Float(t, i, "availability"),
		}
		if got, want := l.TotalPct(), tbl.Float(t, i, "total"); math.Abs(got-want) > 1e-7 {
			t.Errorf("row %d: TotalPct = %.8f, want %.8f", i, got, want)
		}
	}
	if got := power.DefaultLosses.TotalPct(); math.Abs(got-14.07566069) > 1e-7 {
		t.Errorf("DefaultLosses.TotalPct = %.8f, want 14.07566069", got)
	}
}

func TestLossesValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		l    power.Losses
		ok   bool
	}{
		{"defaults", power.DefaultLosses, true},
		{"none", power.Losses{}, true},
		{"single total", power.Losses{Other: 14}, true},
		{"negative", power.Losses{Soiling: -1}, false},
		{"100 percent", power.Losses{Availability: 100}, false},
		{"NaN", power.Losses{Wiring: math.NaN()}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.l.Validate()
			if tt.ok != (err == nil) || (err != nil && !errors.Is(err, power.ErrInvalidParameters)) {
				t.Errorf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
	if f := (power.Losses{Other: 14}).Factor(); math.Abs(f-0.86) > 1e-12 {
		t.Errorf("Factor with 14 %% total = %g, want 0.86", f)
	}
}

func TestInverter(t *testing.T) {
	t.Parallel()
	inv := power.Inverter{RatedACW: 5000}
	tests := []struct {
		name        string
		pdc         float64
		wantPac     float64 // NaN: only range-checked
		wantClipped bool
	}{
		{"no input", 0, 0, false},
		{"negative input", -100, 0, false},
		{"NaN input", math.NaN(), 0, false},
		{"tiny input below self-consumption", 1, 0, false},
		{"heavy oversizing clips", 9000, 5000, true},
		{"half load", 2600, math.NaN(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pac, clipped := inv.AC(tt.pdc)
			if clipped != tt.wantClipped {
				t.Errorf("clipped = %v, want %v", clipped, tt.wantClipped)
			}
			if math.IsNaN(tt.wantPac) {
				if eff := pac / tt.pdc; eff < 0.9 || eff > 0.99 {
					t.Errorf("efficiency %g outside [0.9, 0.99]", eff)
				}
				return
			}
			if pac != tt.wantPac {
				t.Errorf("AC(%g) = %g, want %g", tt.pdc, pac, tt.wantPac)
			}
		})
	}
}

func TestInverterValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		inv power.Inverter
		ok  bool
	}{
		{power.Inverter{RatedACW: 1000}, true},
		{power.Inverter{RatedACW: 1000, EtaNom: 0.98, EtaRef: 0.985}, true},
		{power.Inverter{}, false},
		{power.Inverter{RatedACW: math.Inf(1)}, false},
		{power.Inverter{RatedACW: 1000, EtaNom: 1.2}, false},
		{power.Inverter{RatedACW: 1000, EtaRef: -0.5}, false},
	}
	for _, tt := range tests {
		if err := tt.inv.Validate(); tt.ok != (err == nil) || (err != nil && !errors.Is(err, power.ErrInvalidParameters)) {
			t.Errorf("%+v: Validate() = %v, want ok=%v", tt.inv, err, tt.ok)
		}
	}
}

func TestIAMProperties(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"physical", "ashrae", "none", ""} {
		m, err := power.IAMByName(name)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			t.Parallel()
			if got := m.Modifier(0); math.Abs(got-1) > 1e-12 {
				t.Errorf("Modifier(0) = %g, want 1", got)
			}
			prev := 1.0
			for aoi := 0.0; aoi < 90; aoi += 0.5 {
				v := m.Modifier(aoi)
				if v < 0 || v > 1 || v > prev+1e-12 {
					t.Fatalf("Modifier(%g) = %g: not monotonic in [0, 1]", aoi, v)
				}
				prev = v
			}
			for _, aoi := range []float64{90, 135, 180, math.NaN()} {
				if got := m.Modifier(aoi); got != 0 {
					t.Errorf("Modifier(%g) = %g, want 0", aoi, got)
				}
			}
		})
	}
	if _, err := power.IAMByName("martin_ruiz"); !errors.Is(err, power.ErrInvalidParameters) {
		t.Errorf("unknown IAM: err = %v", err)
	}
}

var sinkF float64

func BenchmarkPipelineStep(b *testing.B) {
	inv := power.Inverter{RatedACW: 9090}
	b.ReportAllocs()
	for b.Loop() {
		geff := 820*power.DefaultPhysicalIAM.Modifier(28) + 140
		pdc := power.PVWattsDC(geff, 47, 10000, power.DefaultGammaPdc) * power.DefaultLosses.Factor()
		sinkF, _ = inv.AC(pdc)
	}
}

func ExampleInverter_AC() {
	// A 10 kWp array on a 9.09 kW inverter (DC/AC ratio 1.1).
	inv := power.Inverter{RatedACW: 9090}
	for _, pdc := range []float64{2000, 6000, 10500} {
		pac, clipped := inv.AC(pdc)
		fmt.Printf("DC %5.0f W -> AC %4.0f W clipped=%v\n", pdc, pac, clipped)
	}
	// Output:
	// DC  2000 W -> AC 1902 W clipped=false
	// DC  6000 W -> AC 5775 W clipped=false
	// DC 10500 W -> AC 9090 W clipped=true
}

func ExampleLosses_TotalPct() {
	fmt.Printf("PVWatts default losses: %.2f %%\n", power.DefaultLosses.TotalPct())
	// Output: PVWatts default losses: 14.08 %
}
