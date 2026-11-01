package thermal_test

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/kviatkovsky/pvforecast-go/internal/testutil"
	"github.com/kviatkovsky/pvforecast-go/thermal"
)

func TestCellTempMatchesPvlib(t *testing.T) {
	t.Parallel()
	tbl := testutil.LoadCSV(t, "thermal.csv")
	errs := map[string]*testutil.MaxErr{}
	for i := range tbl.Rows {
		name := tbl.String(t, i, "model")
		m, err := thermal.ByName(name)
		if err != nil {
			t.Fatal(err)
		}
		poa, ta, ws := tbl.Float(t, i, "poa"), tbl.Float(t, i, "temp_air"), tbl.Float(t, i, "wind")
		e, ok := errs[name]
		if !ok {
			e = &testutil.MaxErr{Name: name, Tol: 1e-7}
			errs[name] = e
		}
		e.Observe(m.CellTemp(poa, ta, ws), tbl.Float(t, i, "cell_temp"), fmt.Sprintf("E=%g Ta=%g WS=%g", poa, ta, ws))
	}
	for _, name := range thermal.Names() {
		e, ok := errs[name]
		if !ok {
			t.Errorf("fixture has no rows for %s", name)
			continue
		}
		e.Report(t)
	}
}

func TestCellTempProperties(t *testing.T) {
	t.Parallel()
	for _, name := range thermal.Names() {
		m, _ := thermal.ByName(name)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := m.Validate(); err != nil {
				t.Fatalf("predefined model invalid: %v", err)
			}
			if got := m.CellTemp(0, 17, 3); got != 17 {
				t.Errorf("at night cell temp = %g, want air temperature 17", got)
			}
			if got := m.CellTemp(-5, 17, -2); got != 17 {
				t.Errorf("negative inputs: cell temp = %g, want 17", got)
			}
			if hot, cold := m.CellTemp(1000, 25, 1), m.CellTemp(1000, 25, 8); !(hot > cold) {
				t.Errorf("wind does not cool: %g at 1 m/s vs %g at 8 m/s", hot, cold)
			}
			if lo, hi := m.CellTemp(300, 25, 2), m.CellTemp(900, 25, 2); !(hi > lo) {
				t.Errorf("irradiance does not heat: %g vs %g", lo, hi)
			}
			// At NOCT-like conditions the rise above ambient ranges from about
			// 20 K (open rack) to about 45 K (insulated back, as in BIPV).
			if rise := m.CellTemp(800, 20, 1) - 20; rise < 15 || rise > 50 {
				t.Errorf("rise at 800 W/m², 1 m/s = %.1f K, outside 15–50 K", rise)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	nan, inf := math.NaN(), math.Inf(1)
	tests := []struct {
		name  string
		model thermal.Model
		ok    bool
	}{
		{"default Faiman", thermal.DefaultFaiman, true},
		{"Faiman zero value", thermal.Faiman{}, false},
		{"Faiman negative U1", thermal.Faiman{U0: 25, U1: -1}, false},
		{"Faiman NaN U0", thermal.Faiman{U0: nan, U1: 6}, false},
		{"Faiman infinite U1", thermal.Faiman{U0: 25, U1: inf}, false},
		{"Faiman wind-independent", thermal.Faiman{U0: 29, U1: 0}, true},
		{"SAPM open rack", thermal.SAPMOpenRackGlassGlass, true},
		{"SAPM positive b", thermal.SAPM{A: -3.5, B: 0.01, DeltaT: 3}, false},
		{"SAPM negative ΔT", thermal.SAPM{A: -3.5, B: -0.05, DeltaT: -1}, false},
		{"SAPM NaN a", thermal.SAPM{A: nan, B: -0.05}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.model.Validate()
			if tt.ok != (err == nil) {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, thermal.ErrInvalidParameters) {
				t.Errorf("error %v does not wrap ErrInvalidParameters", err)
			}
		})
	}
}

func TestByName(t *testing.T) {
	t.Parallel()
	if m, err := thermal.ByName("  FAIMAN "); err != nil || m != thermal.DefaultFaiman {
		t.Errorf("ByName(FAIMAN) = %v, %v", m, err)
	}
	if _, err := thermal.ByName("noct"); !errors.Is(err, thermal.ErrUnknownModel) {
		t.Errorf("unknown name: err = %v", err)
	}
	if !slices.IsSorted(thermal.Names()) || len(thermal.Names()) != 5 {
		t.Errorf("Names() = %v", thermal.Names())
	}
}

func TestWindAtHeight(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		ws, from, to, alpha float64
		want                float64
	}{
		{"same height", 5, 10, 10, thermal.OpenTerrainShear, 5},
		{"10 m to 2 m", 5, 10, 2, thermal.OpenTerrainShear, 5 * math.Pow(0.2, 1.0/7)},
		{"invalid height leaves wind unchanged", 5, 0, 2, thermal.OpenTerrainShear, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := thermal.WindAtHeight(tt.ws, tt.from, tt.to, tt.alpha); math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("WindAtHeight = %g, want %g", got, tt.want)
			}
		})
	}
}

var sinkTemp float64

func BenchmarkCellTemp(b *testing.B) {
	for _, name := range []string{"faiman", "sapm_open_rack_glass_polymer"} {
		m, _ := thermal.ByName(name)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkTemp = m.CellTemp(850, 24, 3.2)
			}
		})
	}
}

func ExampleSAPM_CellTemp() {
	tc := thermal.SAPMOpenRackGlassPolymer.CellTemp(1000, 25, 1)
	fmt.Printf("cell temperature %.1f °C\n", tc)
	// Output: cell temperature 54.4 °C
}
