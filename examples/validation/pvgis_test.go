package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The responses below are hand-written in the PVGIS seriescalc JSON format
// (outputs.hourly[], "YYYYMMDD:HHMM" UTC timestamps, as parsed by pvlib's
// iotools.get_pvgis_hourly). They are not real PVGIS data.
const horizontalJSON = `{"outputs":{"hourly":[
 {"time":"20200621:0010","Gb(i)":0,"Gd(i)":0,"Gr(i)":0,"H_sun":-14.2,"T2m":14.1,"WS10m":1.2,"Int":0},
 {"time":"20200621:1010","Gb(i)":640.0,"Gd(i)":180.0,"Gr(i)":0,"H_sun":62.5,"T2m":24.3,"WS10m":2.9,"Int":0},
 {"time":"20200621:1110","Gb(i)":610.0,"Gd(i)":190.0,"Gr(i)":0,"H_sun":63.1,"T2m":25.0,"WS10m":3.1,"Int":0}
]}}`

const tiltedJSON = `{"outputs":{"hourly":[
 {"time":"20200621:0010","P":0,"Gb(i)":0,"Gd(i)":0,"Gr(i)":0,"H_sun":-14.2,"T2m":14.1,"WS10m":1.2,"Int":0},
 {"time":"20200621:1010","P":730.0,"Gb(i)":690.0,"Gd(i)":175.0,"Gr(i)":9.0,"H_sun":62.5,"T2m":24.3,"WS10m":2.9,"Int":0},
 {"time":"20200621:1110","P":715.0,"Gb(i)":665.0,"Gd(i)":183.0,"Gr(i)":9.0,"H_sun":63.1,"T2m":25.0,"WS10m":3.1,"Int":0}
]}}`

func TestToSeries(t *testing.T) {
	t.Parallel()
	rows, err := parseHourly([]byte(horizontalJSON))
	if err != nil {
		t.Fatal(err)
	}
	s, err := toSeries(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Samples) != 3 {
		t.Fatalf("%d samples", len(s.Samples))
	}
	night, noon := s.Samples[0], s.Samples[1]
	if night.DNI != 0 || night.GHI != 0 {
		t.Errorf("night sample %+v", night)
	}
	if noon.GHI != 820 || noon.DHI != 180 {
		t.Errorf("GHI %g, DHI %g", noon.GHI, noon.DHI)
	}
	if want := 640 / math.Sin(62.5*math.Pi/180); math.Abs(noon.DNI-want) > 1e-9 {
		t.Errorf("DNI %g, want %g", noon.DNI, want)
	}
	if noon.Time.Hour() != 10 || noon.Time.Minute() != 10 {
		t.Errorf("time %v", noon.Time)
	}
}

func TestParseHourlyErrors(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{`, `{"outputs":{"hourly":[]}}`, `{"outputs":{"hourly":[{"time":"2020-06-21 10:10"}]}}`} {
		if _, err := parseHourly([]byte(body)); err == nil {
			t.Errorf("parseHourly(%s) succeeded", body)
		}
	}
}

func TestErrorMetrics(t *testing.T) {
	t.Parallel()
	// Night pairs (0, 0) are skipped; the rest have errors +10, −10, +20.
	m := errorMetrics([]float64{0, 110, 190, 220}, []float64{0, 100, 200, 200})
	if m.N != 3 {
		t.Fatalf("N = %d, want 3", m.N)
	}
	checks := map[string][2]float64{
		"MAE":     {m.MAE, 40.0 / 3},
		"RMSE":    {m.RMSE, math.Sqrt(600.0 / 3)},
		"MBE":     {m.MBE, 20.0 / 3},
		"RefMean": {m.RefMean, 500.0 / 3},
	}
	for name, c := range checks {
		if math.Abs(c[0]-c[1]) > 1e-12 {
			t.Errorf("%s = %g, want %g", name, c[0], c[1])
		}
	}
	if m.ModelTotal != 520 || m.RefTotal != 500 {
		t.Errorf("totals %g, %g", m.ModelTotal, m.RefTotal)
	}
	if z := errorMetrics([]float64{0}, []float64{0}); z.N != 0 || z.MAE != 0 {
		t.Errorf("all-night metrics %+v", z)
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h, tl := filepath.Join(dir, "h.json"), filepath.Join(dir, "t.json")
	for path, body := range map[string]string{h: horizontalJSON, tl: tiltedJSON} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := compare(h, tl)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"POA irradiance", "PV power (DC)", "Annual yield"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}

	short := filepath.Join(dir, "short.json")
	if err := os.WriteFile(short, []byte(`{"outputs":{"hourly":[{"time":"20200621:0010","P":0}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compare(h, short); err == nil {
		t.Error("mismatched row counts accepted")
	}
	night := filepath.Join(dir, "night.json")
	nightJSON := `{"outputs":{"hourly":[{"time":"20200621:0010","P":0,"H_sun":-14,"T2m":14,"WS10m":1}]}}`
	if err := os.WriteFile(night, []byte(nightJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compare(night, night); err == nil {
		t.Error("reference without daytime values accepted")
	}
	if _, err := compare(filepath.Join(dir, "missing.json"), tl); err == nil {
		t.Error("missing file accepted")
	}
}
