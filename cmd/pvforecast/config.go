package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/kviatkovsky/pvforecast-go/forecast"
	"github.com/kviatkovsky/pvforecast-go/irradiance"
	"github.com/kviatkovsky/pvforecast-go/power"
	"github.com/kviatkovsky/pvforecast-go/thermal"
)

// fileConfig is the JSON configuration format. See examples/system.json.
type fileConfig struct {
	Name      string            `json:"name"`
	Lat       *float64          `json:"lat"`
	Lon       *float64          `json:"lon"`
	AltitudeM float64           `json:"altitude_m"`
	Albedo    float64           `json:"albedo"`
	Model     *irradiance.Model `json:"model"`
	Arrays    []arrayConfig     `json:"arrays"`
	Inverter  inverterConfig    `json:"inverter"`
	// Losses lists the PVWatts loss categories in percent; omitted
	// categories are zero. If the whole object is omitted, the PVWatts
	// defaults (14.08 %) apply.
	Losses *power.Losses `json:"losses"`
	// LossesPct is a single total loss in percent, an alternative to Losses.
	LossesPct *float64 `json:"losses_pct"`
}

type arrayConfig struct {
	Name       string  `json:"name"`
	KWp        float64 `json:"kwp"`
	TiltDeg    float64 `json:"tilt"`
	AzimuthDeg float64 `json:"azimuth"`
	GammaPdc   float64 `json:"gamma_pdc"`
	Thermal    string  `json:"thermal"`
	IAM        string  `json:"iam"`
}

type inverterConfig struct {
	ACkW      float64 `json:"ac_kw"`
	DCACRatio float64 `json:"dc_ac_ratio"`
	EtaNom    float64 `json:"eta_nom"`
}

// loadConfig reads and decodes a configuration file, rejecting unknown
// fields so that typos are reported instead of silently ignored.
func loadConfig(path string) (fileConfig, error) {
	f, err := os.Open(path) //nolint:gosec // reading the file the user named in --config is the intent
	if err != nil {
		return fileConfig{}, err
	}
	defer func() { _ = f.Close() }()
	return decodeConfig(f)
}

func decodeConfig(r io.Reader) (fileConfig, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var c fileConfig
	if err := dec.Decode(&c); err != nil {
		return fileConfig{}, fmt.Errorf("decode config: %w", err)
	}
	return c, nil
}

// system converts the configuration into a forecast.System. defaultDCAC
// applies when the inverter specifies neither ac_kw nor dc_ac_ratio.
func (c fileConfig) system(defaultDCAC float64) (forecast.System, error) {
	if c.Lat == nil || c.Lon == nil {
		return forecast.System{}, errors.New("config: lat and lon are required")
	}
	if c.Losses != nil && c.LossesPct != nil {
		return forecast.System{}, errors.New("config: set either losses or losses_pct, not both")
	}
	sys := forecast.System{
		Name: c.Name, Lat: *c.Lat, Lon: *c.Lon, AltitudeM: c.AltitudeM, Albedo: c.Albedo,
		Losses: power.DefaultLosses,
	}
	switch {
	case c.Losses != nil:
		sys.Losses = *c.Losses
	case c.LossesPct != nil:
		sys.Losses = power.Losses{Other: *c.LossesPct}
	}
	for i, a := range c.Arrays {
		arr := forecast.Array{
			Name: a.Name, KWp: a.KWp, TiltDeg: a.TiltDeg, AzimuthDeg: a.AzimuthDeg, GammaPdc: a.GammaPdc,
		}
		if a.Thermal != "" {
			m, err := thermal.ByName(a.Thermal)
			if err != nil {
				return forecast.System{}, fmt.Errorf("config: arrays[%d]: %w", i, err)
			}
			arr.Thermal = m
		}
		if a.IAM != "" {
			m, err := power.IAMByName(a.IAM)
			if err != nil {
				return forecast.System{}, fmt.Errorf("config: arrays[%d]: %w", i, err)
			}
			arr.IAM = m
		}
		sys.Arrays = append(sys.Arrays, arr)
	}
	inv, err := c.Inverter.inverter(sys.DCkWp(), defaultDCAC)
	if err != nil {
		return forecast.System{}, err
	}
	sys.Inverter = inv
	return sys, nil
}

func (c inverterConfig) inverter(dcKWp, defaultDCAC float64) (power.Inverter, error) {
	if c.ACkW != 0 && c.DCACRatio != 0 {
		return power.Inverter{}, errors.New("config: inverter: set either ac_kw or dc_ac_ratio, not both")
	}
	ac := c.ACkW
	if ac == 0 {
		ratio := c.DCACRatio
		if ratio == 0 {
			ratio = defaultDCAC
		}
		if !(ratio > 0) {
			return power.Inverter{}, fmt.Errorf("config: inverter: DC/AC ratio %g must be positive", ratio)
		}
		ac = dcKWp / ratio
	}
	return power.Inverter{RatedACW: ac * 1000, EtaNom: c.EtaNom}, nil
}
