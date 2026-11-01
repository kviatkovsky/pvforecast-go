package power

import (
	"errors"
	"fmt"
	"math"
)

// ErrInvalidParameters reports a loss, efficiency or rating outside its
// valid range.
var ErrInvalidParameters = errors.New("power: invalid parameters")

// Losses are the PVWatts system loss categories, each in percent (Dobos,
// 2014, "PVWatts Version 5 Manual", NREL/TP-6A20-62641).
//
// They combine multiplicatively, so 2 % soiling and 3 % shading remove
// 1 − 0.98·0.97 = 4.94 %, not 5 %.
type Losses struct {
	Soiling      float64 `json:"soiling"`
	Shading      float64 `json:"shading"`
	Snow         float64 `json:"snow"`
	Mismatch     float64 `json:"mismatch"`
	Wiring       float64 `json:"wiring"`
	Connections  float64 `json:"connections"`
	LID          float64 `json:"lid"`       // light-induced degradation
	Nameplate    float64 `json:"nameplate"` // nameplate rating tolerance
	Age          float64 `json:"age"`
	Availability float64 `json:"availability"`
	// Other is any loss outside the PVWatts categories, or the single total
	// when no breakdown is known (set the other fields to zero).
	Other float64 `json:"other"`
}

// DefaultLosses are the PVWatts default losses, 14.08 % in total.
var DefaultLosses = Losses{
	Soiling: 2, Shading: 3, Snow: 0, Mismatch: 2, Wiring: 2,
	Connections: 0.5, LID: 1.5, Nameplate: 1, Age: 0, Availability: 3,
}

func (l Losses) values() [11]float64 {
	return [11]float64{
		l.Soiling, l.Shading, l.Snow, l.Mismatch, l.Wiring, l.Connections,
		l.LID, l.Nameplate, l.Age, l.Availability, l.Other,
	}
}

// Factor returns the fraction of DC power that remains after all losses,
// Π(1 − lᵢ/100).
func (l Losses) Factor() float64 {
	f := 1.0
	for _, v := range l.values() {
		f *= 1 - v/100
	}
	return f
}

// TotalPct returns the combined loss in percent, 100·(1 − Factor()).
func (l Losses) TotalPct() float64 { return 100 * (1 - l.Factor()) }

// Validate reports whether every loss is in [0, 100).
func (l Losses) Validate() error {
	names := [...]string{
		"soiling", "shading", "snow", "mismatch", "wiring", "connections",
		"LID", "nameplate", "age", "availability", "other",
	}
	for i, v := range l.values() {
		if !(v >= 0 && v < 100) {
			return fmt.Errorf("%w: %s loss %g %% outside [0, 100)", ErrInvalidParameters, names[i], v)
		}
	}
	return nil
}

// RefTempC is the reference cell temperature of the PVWatts DC model, °C.
const RefTempC = 25.0

// DefaultGammaPdc is the PVWatts default temperature coefficient of power
// for standard crystalline silicon modules, 1/°C.
const DefaultGammaPdc = -0.004

// PVWattsDC returns DC power in the units of pdc0 for effective
// plane-of-array irradiance geff (W/m²) and cell temperature (°C), using
// the PVWatts v5 model (Dobos, 2014):
//
//	Pdc = Pdc0 · (Geff/1000) · (1 + γ·(Tcell − 25 °C))
//
// pdc0 is the nameplate DC rating at 1000 W/m² and 25 °C; gamma is the
// temperature coefficient in 1/°C (typically −0.003 to −0.005). Negative
// results, which only occur for unphysical inputs, are clipped to zero.
func PVWattsDC(geff, tcellC, pdc0, gamma float64) float64 {
	return math.Max(0, pdc0*geff/1000*(1+gamma*(tcellC-RefTempC)))
}
