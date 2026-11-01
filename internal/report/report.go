// Package report defines the JSON representation of a forecast shared by
// the pvforecast CLI (--format json) and the HTTP API.
package report

import (
	"math"
	"time"

	"github.com/kviatkovsky/pvforecast-go/forecast"
)

// Point is one hourly row of a Report.
type Point struct {
	Time      time.Time `json:"time"`
	POA       float64   `json:"poa_wm2"`
	CellTempC float64   `json:"cell_temp_c"`
	DCkW      float64   `json:"dc_kw"`
	ACkW      float64   `json:"ac_kw"`
	Clipped   bool      `json:"clipped,omitempty"`
}

// Report is a forecast with the metadata needed to read it.
type Report struct {
	System   string                 `json:"system"`
	KWp      float64                `json:"kwp"`
	Source   string                 `json:"source"`
	Label    string                 `json:"label"`
	Interval string                 `json:"interval"`
	Model    string                 `json:"model"`
	TimeZone string                 `json:"time_zone"`
	Points   []Point                `json:"points"`
	Daily    []forecast.DailyEnergy `json:"daily"`
	TotalKWh float64                `json:"total_kwh"`
	PeakACkW float64                `json:"peak_ac_kw"`
}

// New builds a Report. Times are shown in opt.Location (UTC if nil).
//
// Every number is rounded to display precision: full-precision floats
// differ in the last bits between amd64 and arm64 (where the compiler fuses
// multiply-adds), which would make the output platform-dependent.
func New(sys *forecast.System, opt *forecast.Options, res *forecast.Result) Report {
	loc := opt.Location
	if loc == nil {
		loc = time.UTC
	}
	r := Report{
		System: sys.Name, KWp: sys.DCkWp(), Source: res.Source,
		Label: res.Label.String(), Interval: res.Interval.String(),
		Model: opt.Transposition.String(), TimeZone: loc.String(),
		Points:   make([]Point, len(res.Points)),
		Daily:    make([]forecast.DailyEnergy, len(res.Daily)),
		TotalKWh: Round(res.TotalKWh, 3), PeakACkW: Round(res.PeakACkW, 4),
	}
	for i, p := range res.Points {
		r.Points[i] = Point{
			Time: p.Time.In(loc), POA: Round(p.POA, 1), CellTempC: Round(p.CellTempC, 2),
			DCkW: Round(p.DCkW, 4), ACkW: Round(p.ACkW, 4), Clipped: p.Clipped,
		}
	}
	for i, d := range res.Daily {
		r.Daily[i] = forecast.DailyEnergy{Date: d.Date, KWh: Round(d.KWh, 3)}
	}
	return r
}

// Round rounds v to the given number of decimals for display.
func Round(v float64, decimals int) float64 {
	p := math.Pow10(decimals)
	return math.Round(v*p) / p
}
