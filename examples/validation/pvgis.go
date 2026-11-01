package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/kviatkovsky/pvforecast-go/forecast"
	"github.com/kviatkovsky/pvforecast-go/weather"
)

// hourly is one row of a PVGIS seriescalc JSON response
// (outputs.hourly[]). Field names follow the PVGIS API; with components=1
// the plane-of-array irradiance is split into beam, diffuse and reflected.
type hourly struct {
	Time   string  `json:"time"` // "20200101:0010", UTC
	P      float64 `json:"P"`    // PV output, W (only with pvcalculation=1)
	Gb     float64 `json:"Gb(i)"`
	Gd     float64 `json:"Gd(i)"`
	Gr     float64 `json:"Gr(i)"`
	HSun   float64 `json:"H_sun"` // solar elevation, degrees
	T2m    float64 `json:"T2m"`
	WS10m  float64 `json:"WS10m"`
	parsed time.Time
}

func readHourly(path string) ([]hourly, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the path is a command-line argument
	if err != nil {
		return nil, err
	}
	return parseHourly(b)
}

func parseHourly(b []byte) ([]hourly, error) {
	var doc struct {
		Outputs struct {
			Hourly []hourly `json:"hourly"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("pvgis: decode: %w", err)
	}
	rows := doc.Outputs.Hourly
	if len(rows) == 0 {
		return nil, errors.New("pvgis: response has no outputs.hourly rows")
	}
	for i := range rows {
		t, err := time.Parse("20060102:1504", rows[i].Time)
		if err != nil {
			return nil, fmt.Errorf("pvgis: row %d: %w", i, err)
		}
		rows[i].parsed = t
	}
	return rows, nil
}

// minElevation is the solar elevation, in degrees, below which beam
// irradiance is not converted to DNI: dividing by sin(elevation) would
// amplify noise.
const minElevation = 2.0

// toSeries converts a horizontal-plane (angle=0) response into weather
// samples. On a horizontal plane Gb(i) is the beam horizontal and Gd(i) the
// diffuse horizontal irradiance, so GHI = Gb + Gd and DNI = Gb / sin(h).
// PVGIS timestamps (HH:10) mark the satellite observation, so samples are
// treated as instantaneous.
func toSeries(rows []hourly) (weather.Series, error) {
	s := weather.Series{Interval: time.Hour, Label: weather.Instant, Source: "pvgis"}
	for _, r := range rows {
		var dni float64
		if r.HSun > minElevation {
			dni = r.Gb / math.Sin(r.HSun*math.Pi/180)
		}
		s.Samples = append(s.Samples, weather.Sample{
			Time: r.parsed, GHI: r.Gb + r.Gd, DNI: dni, DHI: r.Gd, AirTempC: r.T2m, WindMS: r.WS10m,
		})
	}
	return s, s.Validate()
}

// metrics are hourly error statistics of model output against a
// reference, in the reference's units.
type metrics struct {
	N                    int
	MAE, RMSE, MBE       float64
	RefMean              float64
	ModelTotal, RefTotal float64
}

func (m metrics) normalized(v float64) float64 { return 100 * v / m.RefMean }

// errorMetrics compares paired values, skipping pairs where both are zero
// (night), which would otherwise dilute the statistics.
func errorMetrics(model, ref []float64) metrics {
	var m metrics
	var sumAbs, sumSq, sumErr float64
	for i := range ref {
		m.ModelTotal += model[i]
		m.RefTotal += ref[i]
		if model[i] == 0 && ref[i] == 0 {
			continue
		}
		e := model[i] - ref[i]
		sumAbs += math.Abs(e)
		sumSq += e * e
		sumErr += e
		m.RefMean += ref[i]
		m.N++
	}
	if m.N == 0 {
		return m
	}
	n := float64(m.N)
	m.MAE, m.RMSE, m.MBE, m.RefMean = sumAbs/n, math.Sqrt(sumSq/n), sumErr/n, m.RefMean/n
	return m
}

func summarize(res forecast.Result, tilted []hourly) (string, error) {
	if len(tilted) != len(res.Points) {
		return "", fmt.Errorf("pvgis: %d tilted rows for %d horizontal rows", len(tilted), len(res.Points))
	}
	modelP := make([]float64, len(tilted))
	refP := make([]float64, len(tilted))
	modelPOA := make([]float64, len(tilted))
	refPOA := make([]float64, len(tilted))
	for i, p := range res.Points {
		if !p.Time.Equal(tilted[i].parsed) {
			return "", fmt.Errorf("pvgis: row %d: time %s does not match %s", i, tilted[i].parsed, p.Time)
		}
		modelP[i] = p.DCkW * 1000
		refP[i] = tilted[i].P
		modelPOA[i] = p.POA
		refPOA[i] = tilted[i].Gb + tilted[i].Gd + tilted[i].Gr
	}
	pw, poa := errorMetrics(modelP, refP), errorMetrics(modelPOA, refPOA)
	if pw.RefTotal <= 0 || poa.RefTotal <= 0 {
		return "", errors.New("pvgis: reference data has no daytime values")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Site %.4f, %.4f · 1 kWp, tilt %d°, south · %d %% losses · year %d · %d hours\n\n",
		lat, lon, tiltDeg, lossPct, year, len(tilted))
	fmt.Fprintf(&b, "%-18s %10s %10s %10s %12s\n", "quantity", "MAE", "RMSE", "MBE", "annual diff")
	row := func(name string, m metrics, unit string) {
		fmt.Fprintf(&b, "%-18s %7.1f %s %7.1f %s %7.1f %s %11.1f %%\n", name,
			m.MAE, unit, m.RMSE, unit, m.MBE, unit, 100*(m.ModelTotal-m.RefTotal)/m.RefTotal)
		fmt.Fprintf(&b, "%-18s %8.1f %% %8.1f %% %8.1f %%   (normalized by mean daytime value)\n", "",
			m.normalized(m.MAE), m.normalized(m.RMSE), m.normalized(m.MBE))
	}
	row("POA irradiance", poa, "W/m²")
	row("PV power (DC)", pw, "W   ")
	fmt.Fprintf(&b, "\nAnnual yield: pvforecast-go %.0f kWh/kWp, PVGIS %.0f kWh/kWp\n", pw.ModelTotal/1000, pw.RefTotal/1000)
	return b.String(), nil
}
