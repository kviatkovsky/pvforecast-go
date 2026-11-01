package power

import (
	"fmt"
	"math"
	"strings"
)

// IAM is an incidence angle modifier: the fraction of beam irradiance that
// reaches the cells, relative to normal incidence, after reflection and
// absorption in the module cover.
type IAM interface {
	// Modifier returns the modifier in [0, 1] for an angle of incidence in
	// degrees. Angles of 90° or more give zero.
	Modifier(aoiDeg float64) float64
}

// PhysicalIAM models a single glass cover with Fresnel reflection and
// Bouguer absorption (De Soto, Klein & Beckman, 2006, "Improvement and
// validation of a model for photovoltaic array performance", Solar Energy
// 80(1)):
//
//	θr = asin(sin θ / n)
//	τ(θ) = e^(−K·L/cos θr) · [1 − ½·(sin²(θr−θ)/sin²(θr+θ) + tan²(θr−θ)/tan²(θr+θ))]
//	IAM = τ(θ)/τ(0)
type PhysicalIAM struct {
	N float64 // refractive index of the cover
	K float64 // extinction coefficient, 1/m
	L float64 // cover thickness, m
}

// DefaultPhysicalIAM is the glass cover used by PVWatts v5 (Dobos, 2014):
// n = 1.526, K = 4 /m, L = 2 mm.
var DefaultPhysicalIAM = PhysicalIAM{N: 1.526, K: 4, L: 0.002}

// Modifier implements IAM.
func (m PhysicalIAM) Modifier(aoiDeg float64) float64 {
	if !(aoiDeg < 90) {
		return 0
	}
	theta := math.Abs(aoiDeg) * math.Pi / 180
	return m.transmittance(theta) / m.transmittance(0)
}

// transmittance returns τ(θ) for an angle of incidence in radians. At
// normal incidence the Fresnel terms are 0/0; their limit is ((n−1)/(n+1))².
func (m PhysicalIAM) transmittance(theta float64) float64 {
	absorption := func(cosR float64) float64 { return math.Exp(-m.K * m.L / cosR) }
	if theta < 1e-6 {
		r := (m.N - 1) / (m.N + 1)
		return absorption(1) * (1 - r*r)
	}
	thetaR := math.Asin(math.Sin(theta) / m.N)
	s := math.Sin(thetaR-theta) / math.Sin(thetaR+theta)
	t := math.Tan(thetaR-theta) / math.Tan(thetaR+theta)
	return absorption(math.Cos(thetaR)) * (1 - (s*s+t*t)/2)
}

// ASHRAEIAM is the ASHRAE incidence angle modifier (Souka & Safwat, 1966;
// ASHRAE Standard 93-77):
//
//	IAM = 1 − b·(1/cos θ − 1), clipped to [0, 1]
type ASHRAEIAM struct {
	B float64 // typically 0.05
}

// DefaultASHRAEIAM uses b = 0.05.
var DefaultASHRAEIAM = ASHRAEIAM{B: 0.05}

// Modifier implements IAM.
func (m ASHRAEIAM) Modifier(aoiDeg float64) float64 {
	if !(math.Abs(aoiDeg) < 90) {
		return 0
	}
	return math.Max(0, math.Min(1, 1-m.B*(1/math.Cos(aoiDeg*math.Pi/180)-1)))
}

// NoIAM applies no incidence angle losses (modifier 1 below 90°).
type NoIAM struct{}

// Modifier implements IAM.
func (NoIAM) Modifier(aoiDeg float64) float64 {
	if !(math.Abs(aoiDeg) < 90) {
		return 0
	}
	return 1
}

// IAMByName returns a predefined modifier by its configuration name,
// case-insensitively: "physical" (the default), "ashrae" or "none".
func IAMByName(name string) (IAM, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "physical", "":
		return DefaultPhysicalIAM, nil
	case "ashrae":
		return DefaultASHRAEIAM, nil
	case "none":
		return NoIAM{}, nil
	}
	return nil, fmt.Errorf("%w: unknown IAM model %q (want physical, ashrae or none)", ErrInvalidParameters, name)
}
