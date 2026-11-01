package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kviatkovsky/pvforecast-go/forecast"
	"github.com/kviatkovsky/pvforecast-go/weather"
)

// report is everything a renderer needs.
type report struct {
	sys   forecast.System
	opt   forecast.Options
	res   forecast.Result
	night bool
}

type renderFunc func(io.Writer, *report) error

func renderer(format string) (renderFunc, error) {
	switch strings.ToLower(format) {
	case "table":
		return renderTable, nil
	case "json":
		return renderJSON, nil
	case "csv":
		return renderCSV, nil
	}
	return nil, usagef("--format %q: want table, json or csv", format)
}

// period formats the interval a point covers in local time, so that hourly
// means read unambiguously ("11:00–12:00" rather than "12:00").
func period(p forecast.Point, r forecast.Result, loc *time.Location) string {
	t := p.Time.In(loc)
	switch r.Label {
	case weather.IntervalEnd:
		return t.Add(-r.Interval).Format("2006-01-02 15:04") + "–" + t.Format("15:04")
	case weather.IntervalStart:
		return t.Format("2006-01-02 15:04") + "–" + t.Add(r.Interval).Format("15:04")
	default:
		return t.Format("2006-01-02 15:04")
	}
}

func renderTable(w io.Writer, r *report) error {
	sys, res, loc := r.sys, r.res, r.opt.Location
	fmt.Fprintf(w, "%s · %.4f, %.4f · %.1f kWp in %d array(s) · inverter %.2f kW · losses %.1f %%\n",
		sys.Name, sys.Lat, sys.Lon, sys.DCkWp(), len(sys.Arrays), sys.Inverter.RatedACW/1000, sys.Losses.TotalPct())
	fmt.Fprintf(w, "Weather: %s, %s · sky model: %s · times in %s\n\n", res.Source, res.Label, r.opt.Transposition, loc)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "PERIOD\tPOA W/m²\tCELL °C\tDC kW\tAC kW\t")
	for _, p := range res.Points {
		if !r.night && p.POA == 0 && p.ACkW == 0 {
			continue
		}
		clip := ""
		if p.Clipped {
			clip = " (clipped)"
		}
		fmt.Fprintf(tw, "%s\t%.0f\t%.1f\t%.2f\t%.2f%s\t\n", period(p, res, loc), p.POA, p.CellTempC, p.DCkW, p.ACkW, clip)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "DATE\tENERGY kWh\tkWh/kWp\t")
	for _, d := range res.Daily {
		fmt.Fprintf(tw, "%s\t%.1f\t%.2f\t\n", d.Date, d.KWh, d.KWh/sys.DCkWp())
	}
	fmt.Fprintf(tw, "TOTAL\t%.1f\t%.2f\t\n", res.TotalKWh, res.TotalKWh/sys.DCkWp())
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nPeak AC %.2f kW. Weather data © Open-Meteo.com (CC BY 4.0).\n", res.PeakACkW)
	return nil
}

type jsonPoint struct {
	Time      time.Time `json:"time"`
	POA       float64   `json:"poa_wm2"`
	CellTempC float64   `json:"cell_temp_c"`
	DCkW      float64   `json:"dc_kw"`
	ACkW      float64   `json:"ac_kw"`
	Clipped   bool      `json:"clipped,omitempty"`
}

type jsonReport struct {
	System   string                 `json:"system"`
	KWp      float64                `json:"kwp"`
	Source   string                 `json:"source"`
	Label    string                 `json:"label"`
	Interval string                 `json:"interval"`
	Model    string                 `json:"model"`
	TimeZone string                 `json:"time_zone"`
	Points   []jsonPoint            `json:"points"`
	Daily    []forecast.DailyEnergy `json:"daily"`
	TotalKWh float64                `json:"total_kwh"`
	PeakACkW float64                `json:"peak_ac_kw"`
}

func renderJSON(w io.Writer, r *report) error {
	out := jsonReport{
		System: r.sys.Name, KWp: r.sys.DCkWp(), Source: r.res.Source,
		Label: r.res.Label.String(), Interval: r.res.Interval.String(),
		Model: r.opt.Transposition.String(), TimeZone: r.opt.Location.String(),
		Points: make([]jsonPoint, len(r.res.Points)), Daily: make([]forecast.DailyEnergy, len(r.res.Daily)),
		TotalKWh: r.res.TotalKWh, PeakACkW: r.res.PeakACkW,
	}
	for i, p := range r.res.Points {
		out.Points[i] = jsonPoint{
			Time: p.Time.In(r.opt.Location), POA: round(p.POA, 1), CellTempC: round(p.CellTempC, 2),
			DCkW: round(p.DCkW, 4), ACkW: round(p.ACkW, 4), Clipped: p.Clipped,
		}
	}
	for i, d := range r.res.Daily {
		out.Daily[i] = forecast.DailyEnergy{Date: d.Date, KWh: round(d.KWh, 3)}
	}
	out.TotalKWh, out.PeakACkW = round(out.TotalKWh, 3), round(out.PeakACkW, 4)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func renderCSV(w io.Writer, r *report) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"time", "poa_wm2", "cell_temp_c", "dc_kw", "ac_kw", "clipped"}); err != nil {
		return err
	}
	for _, p := range r.res.Points {
		rec := []string{
			p.Time.In(r.opt.Location).Format(time.RFC3339),
			strconv.FormatFloat(round(p.POA, 1), 'f', -1, 64),
			strconv.FormatFloat(round(p.CellTempC, 2), 'f', -1, 64),
			strconv.FormatFloat(round(p.DCkW, 4), 'f', -1, 64),
			strconv.FormatFloat(round(p.ACkW, 4), 'f', -1, 64),
			strconv.FormatBool(p.Clipped),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// round rounds to the given number of decimals for display; the library
// keeps full precision.
func round(v float64, decimals int) float64 {
	p := math.Pow10(decimals)
	return math.Round(v*p) / p
}
