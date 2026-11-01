package thermal

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// ErrInvalidParameters reports model parameters that make the model
// undefined, such as a non-positive heat loss coefficient.
var ErrInvalidParameters = errors.New("thermal: invalid model parameters")

// ErrUnknownModel reports a model name that ByName does not recognize.
var ErrUnknownModel = errors.New("thermal: unknown model")

// Model estimates PV cell temperature.
//
// Implementations must be pure and safe for concurrent use.
type Model interface {
	// CellTemp returns the cell temperature in °C for plane-of-array
	// irradiance poa (W/m²), air temperature airC (°C) and wind speed windMS
	// (m/s) at the height the model was calibrated for.
	CellTemp(poa, airC, windMS float64) float64
	// Validate reports whether the parameters define a usable model.
	Validate() error
}

// Faiman is the module temperature model of Faiman (2008), "Assessing the
// outdoor operating temperature of photovoltaic modules", Progress in
// Photovoltaics 16(4):
//
//	Tc = Ta + E / (U0 + U1·WS)
//
// The heat loss coefficients were fitted to wind measured at module height;
// see ADR 0005 for how wind height is handled.
type Faiman struct {
	U0 float64 // constant heat transfer component, W/(m²·K)
	U1 float64 // convective heat transfer component, W/(m³·s·K)
}

// DefaultFaiman holds the combined fit of Faiman (2008) for seven
// glass/Tedlar modules on an open rack: U0 = 25, U1 = 6.84.
var DefaultFaiman = Faiman{U0: 25, U1: 6.84}

// CellTemp implements Model. Negative irradiance and wind are treated as
// zero.
func (m Faiman) CellTemp(poa, airC, windMS float64) float64 {
	return airC + math.Max(poa, 0)/(m.U0+m.U1*math.Max(windMS, 0))
}

// Validate implements Model.
func (m Faiman) Validate() error {
	if !(m.U0 > 0) || !(m.U1 >= 0) || math.IsInf(m.U0, 0) || math.IsInf(m.U1, 0) {
		return fmt.Errorf("%w: Faiman U0 = %g must be positive and U1 = %g non-negative", ErrInvalidParameters, m.U0, m.U1)
	}
	return nil
}

// SAPM is the Sandia Array Performance Model temperature model of King,
// Boyson & Kratochvil (2004), "Photovoltaic Array Performance Model",
// SAND2004-3535, eq. 11–12:
//
//	Tm = E·exp(a + b·WS) + Ta
//	Tc = Tm + (E/E0)·ΔT,  E0 = 1000 W/m²
//
// The coefficients were determined with wind speed measured at 10 m.
type SAPM struct {
	A      float64 // upper limit of module temperature at low wind, ln(K·m²/W)
	B      float64 // rate of decrease with wind speed, s/m
	DeltaT float64 // cell-to-back temperature difference at 1000 W/m², K
}

// SAPM coefficients for common constructions and mountings (King et al.,
// 2004, Table 2).
var (
	SAPMOpenRackGlassGlass        = SAPM{A: -3.47, B: -0.0594, DeltaT: 3}
	SAPMCloseMountGlassGlass      = SAPM{A: -2.98, B: -0.0471, DeltaT: 1}
	SAPMOpenRackGlassPolymer      = SAPM{A: -3.56, B: -0.0750, DeltaT: 3}
	SAPMInsulatedBackGlassPolymer = SAPM{A: -2.81, B: -0.0455, DeltaT: 0}
)

// sapmRefIrradiance is E0 in the SAPM cell temperature equation, W/m².
const sapmRefIrradiance = 1000.0

// CellTemp implements Model. Negative irradiance and wind are treated as
// zero.
func (m SAPM) CellTemp(poa, airC, windMS float64) float64 {
	poa = math.Max(poa, 0)
	module := poa*math.Exp(m.A+m.B*math.Max(windMS, 0)) + airC
	return module + poa/sapmRefIrradiance*m.DeltaT
}

// Validate implements Model. B must not be positive, otherwise the module
// would heat up as wind increases.
func (m SAPM) Validate() error {
	if math.IsNaN(m.A) || math.IsInf(m.A, 0) || !(m.B <= 0) || math.IsInf(m.B, 0) ||
		!(m.DeltaT >= 0) || math.IsInf(m.DeltaT, 0) {
		return fmt.Errorf("%w: SAPM a = %g, b = %g, ΔT = %g", ErrInvalidParameters, m.A, m.B, m.DeltaT)
	}
	return nil
}

// byName maps the names accepted by ByName to models.
var byName = map[string]Model{
	"faiman":                            DefaultFaiman,
	"sapm_open_rack_glass_glass":        SAPMOpenRackGlassGlass,
	"sapm_close_mount_glass_glass":      SAPMCloseMountGlassGlass,
	"sapm_open_rack_glass_polymer":      SAPMOpenRackGlassPolymer,
	"sapm_insulated_back_glass_polymer": SAPMInsulatedBackGlassPolymer,
}

// ByName returns a predefined model by its configuration name,
// case-insensitively: "faiman" (DefaultFaiman) or "sapm_<mounting>", for
// example "sapm_open_rack_glass_polymer". Names returns the full list.
func ByName(name string) (Model, error) {
	if m, ok := byName[strings.ToLower(strings.TrimSpace(name))]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("%w: %q (known: %s)", ErrUnknownModel, name, strings.Join(Names(), ", "))
}

// Names returns the names accepted by ByName, sorted.
func Names() []string {
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// WindAtHeight scales a wind speed measured at fromM meters to toM meters
// with the power law WS₂ = WS₁·(h₂/h₁)^α. The exponent α = 1/7 is the
// classic value for open, level terrain (Peterson & Hennessey, 1978, "On the
// use of power laws for estimates of wind power potential", J. Appl.
// Meteorol. 17(3)).
//
// It returns windMS unchanged if either height is not positive.
func WindAtHeight(windMS, fromM, toM, alpha float64) float64 {
	if !(fromM > 0) || !(toM > 0) {
		return windMS
	}
	return windMS * math.Pow(toM/fromM, alpha)
}

// OpenTerrainShear is the wind shear exponent α = 1/7 for open, level
// terrain, for use with WindAtHeight.
const OpenTerrainShear = 1.0 / 7
