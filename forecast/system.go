package forecast

import (
	"errors"
	"fmt"
	"math"

	"github.com/kviatkovsky/pvforecast-go/power"
	"github.com/kviatkovsky/pvforecast-go/thermal"
)

// DefaultAlbedo is the ground reflectance used when System.Albedo is zero.
const DefaultAlbedo = 0.25

// defaultThermal is the cell temperature model used when Array.Thermal is
// nil (see ADR 0005). It is not exported so that Compute depends on no
// mutable package state.
var defaultThermal thermal.Model = thermal.SAPMOpenRackGlassPolymer

// ErrInvalidSystem is wrapped by every error that System.Validate returns.
var ErrInvalidSystem = errors.New("forecast: invalid system")

// System is a PV plant: one site, one or more arrays with their own
// orientation, and a single inverter that all arrays feed.
type System struct {
	Name      string
	Lat, Lon  float64 // degrees, positive north and east
	AltitudeM float64 // above mean sea level
	Arrays    []Array
	Inverter  power.Inverter
	Losses    power.Losses
	// Albedo is the ground reflectance in [0, 1]. Zero selects
	// DefaultAlbedo.
	Albedo float64
}

// Array is a group of identical modules with one orientation.
type Array struct {
	Name       string
	KWp        float64 // DC nameplate rating at STC, kW
	TiltDeg    float64 // 0 = horizontal, 90 = vertical
	AzimuthDeg float64 // clockwise from north; 180 = facing south
	// GammaPdc is the temperature coefficient of power, 1/°C. Zero selects
	// power.DefaultGammaPdc (−0.004).
	GammaPdc float64
	// Thermal is the cell temperature model. Nil selects
	// thermal.SAPMOpenRackGlassPolymer.
	Thermal thermal.Model
	// IAM is the incidence angle modifier for beam irradiance. Nil selects
	// power.DefaultPhysicalIAM.
	IAM power.IAM
}

// FieldError describes one invalid field of a System. It unwraps to
// ErrInvalidSystem.
type FieldError struct {
	Field string // for example "arrays[1].tilt"
	Msg   string
}

// Error implements error.
func (e *FieldError) Error() string {
	return fmt.Sprintf("forecast: invalid system: %s: %s", e.Field, e.Msg)
}

// Unwrap returns ErrInvalidSystem.
func (e *FieldError) Unwrap() error { return ErrInvalidSystem }

// Validate checks every field and returns all problems at once, joined with
// errors.Join. Each one is a *FieldError.
func (s System) Validate() error {
	var errs []error
	add := func(field, format string, args ...any) {
		errs = append(errs, &FieldError{Field: field, Msg: fmt.Sprintf(format, args...)})
	}

	if !between(s.Lat, -90, 90) {
		add("lat", "%g is outside [-90, 90]", s.Lat)
	}
	if !between(s.Lon, -180, 180) {
		add("lon", "%g is outside [-180, 180]", s.Lon)
	}
	if !between(s.AltitudeM, -500, 9000) {
		add("altitude_m", "%g is outside [-500, 9000]", s.AltitudeM)
	}
	if !between(s.Albedo, 0, 1) {
		add("albedo", "%g is outside [0, 1]", s.Albedo)
	}
	if len(s.Arrays) == 0 {
		add("arrays", "at least one array is required")
	}
	for i, a := range s.Arrays {
		p := fmt.Sprintf("arrays[%d]", i)
		if !(a.KWp > 0) || math.IsInf(a.KWp, 0) {
			add(p+".kwp", "%g must be positive", a.KWp)
		}
		if !between(a.TiltDeg, 0, 90) {
			add(p+".tilt", "%g is outside [0, 90]", a.TiltDeg)
		}
		if !between(a.AzimuthDeg, 0, 360) {
			add(p+".azimuth", "%g is outside [0, 360]", a.AzimuthDeg)
		}
		if !between(a.GammaPdc, -0.02, 0) {
			add(p+".gamma_pdc", "%g is outside [-0.02, 0] 1/°C", a.GammaPdc)
		}
		if a.Thermal != nil {
			if err := a.Thermal.Validate(); err != nil {
				add(p+".thermal", "%v", err)
			}
		}
	}
	if err := s.Inverter.Validate(); err != nil {
		add("inverter", "%v", err)
	}
	if err := s.Losses.Validate(); err != nil {
		add("losses", "%v", err)
	}
	return errors.Join(errs...)
}

// DCkWp returns the total DC nameplate rating of all arrays.
func (s System) DCkWp() float64 {
	var sum float64
	for _, a := range s.Arrays {
		sum += a.KWp
	}
	return sum
}

func between(v, lo, hi float64) bool { return v >= lo && v <= hi }

// resolved is an Array with defaults applied, so the hot loop does no
// nil checks or zero tests.
type resolved struct {
	pdc0W    float64
	tilt, az float64
	gamma    float64
	thermal  thermal.Model
	iam      power.IAM
}

func (s System) resolve() []resolved {
	out := make([]resolved, len(s.Arrays))
	for i, a := range s.Arrays {
		r := resolved{
			pdc0W: a.KWp * 1000, tilt: a.TiltDeg, az: a.AzimuthDeg,
			gamma: a.GammaPdc, thermal: a.Thermal, iam: a.IAM,
		}
		if r.gamma == 0 {
			r.gamma = power.DefaultGammaPdc
		}
		if r.thermal == nil {
			r.thermal = defaultThermal
		}
		if r.iam == nil {
			r.iam = power.DefaultPhysicalIAM
		}
		out[i] = r
	}
	return out
}

func (s System) albedo() float64 {
	if s.Albedo == 0 {
		return DefaultAlbedo
	}
	return s.Albedo
}
