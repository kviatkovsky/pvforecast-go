package irradiance

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Model selects the sky-diffuse transposition model.
//
// The zero value is Isotropic. Model implements encoding.TextMarshaler and
// encoding.TextUnmarshaler, so it can be used directly in JSON configuration.
type Model int

// Sky-diffuse transposition models.
const (
	// Isotropic assumes uniform sky radiance (Liu & Jordan, 1963).
	Isotropic Model = iota
	// HayDavies adds a circumsolar component weighted by the anisotropy
	// index DNI/DNIExtra (Hay & Davies, 1980).
	HayDavies
	// Perez adds circumsolar and horizon-brightening components with
	// empirical coefficients (Perez et al., 1990), using the
	// "allsitescomposite1990" coefficient set.
	Perez
)

// Sentinel errors.
var (
	// ErrUnknownModel reports a Model value or name that is not defined.
	ErrUnknownModel = errors.New("irradiance: unknown transposition model")
	// ErrInvalidInput reports an input that a model cannot use, such as a
	// non-positive extraterrestrial irradiance.
	ErrInvalidInput = errors.New("irradiance: invalid input")
)

var modelNames = [...]string{Isotropic: "isotropic", HayDavies: "haydavies", Perez: "perez"}

// String returns the lower-case model name.
func (m Model) String() string {
	if m < 0 || int(m) >= len(modelNames) {
		return fmt.Sprintf("Model(%d)", int(m))
	}
	return modelNames[m]
}

// ParseModel parses a model name case-insensitively. "hay-davies" and
// "hay_davies" are accepted as aliases of "haydavies".
func ParseModel(s string) (Model, error) {
	norm := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(s)))
	for i, name := range modelNames {
		if norm == name {
			return Model(i), nil
		}
	}
	return 0, fmt.Errorf("%w: %q (want isotropic, haydavies or perez)", ErrUnknownModel, s)
}

// MarshalText implements encoding.TextMarshaler.
func (m Model) MarshalText() ([]byte, error) {
	if m < 0 || int(m) >= len(modelNames) {
		return nil, fmt.Errorf("%w: %d", ErrUnknownModel, int(m))
	}
	return []byte(modelNames[m]), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *Model) UnmarshalText(text []byte) error {
	v, err := ParseModel(string(text))
	if err != nil {
		return err
	}
	*m = v
	return nil
}

// Input holds everything needed to transpose irradiance onto a tilted
// surface for one instant. Angles are in degrees, irradiance in W/m².
type Input struct {
	TiltDeg           float64 // surface tilt from horizontal, 0–90 for fixed PV
	SurfaceAzimuthDeg float64 // clockwise from north; 180 = facing south
	ZenithDeg         float64 // apparent solar zenith
	SolarAzimuthDeg   float64 // clockwise from north

	GHI, DNI, DHI float64
	// DNIExtra is the extraterrestrial normal irradiance. It must be
	// positive for HayDavies and Perez.
	DNIExtra float64
	// AirMass is the relative air mass used by Perez. Zero means
	// RelativeAirMass(ZenithDeg).
	AirMass float64
	// Albedo is the ground reflectance used for the ground-reflected
	// component, typically 0.2–0.3 (0.25 is a common default).
	Albedo float64
}

// SkyDiffuse is the sky-diffuse irradiance on the plane of array and its
// components. Isotropic + Circumsolar + Horizon = Total. Horizon can be
// negative: the Perez model darkens the horizon band under some skies.
type SkyDiffuse struct {
	Total       float64
	Isotropic   float64
	Circumsolar float64
	Horizon     float64
}

// POA is the irradiance on the plane of array, in W/m².
type POA struct {
	Global        float64 // Beam + Sky.Total + GroundDiffuse
	Beam          float64
	Sky           SkyDiffuse
	GroundDiffuse float64
	AOIDeg        float64 // angle of incidence of the beam, degrees
}

// Transpose computes plane-of-array irradiance with the given sky-diffuse
// model.
//
// Conventions near and below the horizon (see ADR 0004):
//   - Beam and circumsolar irradiance are zero when the sun is behind the
//     surface or at or below the horizon (zenith ≥ 90°).
//   - At or below the horizon the sky diffuse is isotropic for every model,
//     because the anisotropic terms are not defined there.
//   - Negative GHI, DNI and DHI are treated as zero. Unlike pvlib, this
//     keeps a negative DNI from producing beam irradiance when the sun is
//     behind the surface.
//
// Transpose does not allocate.
func Transpose(m Model, in Input) (POA, error) {
	if m < 0 || int(m) >= len(modelNames) {
		return POA{}, fmt.Errorf("%w: %d", ErrUnknownModel, int(m))
	}
	if m != Isotropic && !(in.DNIExtra > 0) {
		return POA{}, fmt.Errorf("%w: DNIExtra %g must be positive for %s", ErrInvalidInput, in.DNIExtra, m)
	}

	// Negative irradiance is measurement or forecast noise. Clipping it
	// before use matters: with the sun behind the surface, DNI·cos θ would
	// otherwise be the product of two negatives.
	in.GHI, in.DNI, in.DHI = math.Max(in.GHI, 0), math.Max(in.DNI, 0), math.Max(in.DHI, 0)

	cosTheta := cosAOI(in.TiltDeg, in.SurfaceAzimuthDeg, in.ZenithDeg, in.SolarAzimuthDeg)
	sunUp := in.ZenithDeg < 90

	var p POA
	p.AOIDeg = rad2deg * math.Acos(cosTheta)
	if sunUp {
		p.Beam = in.DNI * math.Max(0, cosTheta)
	}
	p.GroundDiffuse = GroundDiffuse(in.TiltDeg, in.GHI, in.Albedo)

	switch {
	case !sunUp || m == Isotropic:
		p.Sky = IsotropicSky(in.TiltDeg, in.DHI)
	case m == HayDavies:
		p.Sky = HayDaviesSky(in.TiltDeg, in.DHI, in.DNI, in.DNIExtra, in.ZenithDeg, cosTheta)
	default: // Perez
		am := in.AirMass
		if am == 0 {
			am = RelativeAirMass(in.ZenithDeg)
		}
		p.Sky = PerezSky(in.TiltDeg, in.DHI, in.DNI, in.DNIExtra, in.ZenithDeg, am, cosTheta)
	}

	p.Global = p.Beam + p.Sky.Total + p.GroundDiffuse
	return p, nil
}

// GroundDiffuse returns the ground-reflected irradiance on a surface tilted
// by tiltDeg, assuming an isotropically reflecting ground with the given
// albedo: GHI·ρ·(1 − cos β)/2.
func GroundDiffuse(tiltDeg, ghi, albedo float64) float64 {
	return math.Max(ghi, 0) * albedo * (1 - cosd(tiltDeg)) / 2
}

// IsotropicSky returns the sky diffuse on a tilted surface under uniform
// sky radiance: DHI·(1 + cos β)/2 (Liu & Jordan, 1963).
func IsotropicSky(tiltDeg, dhi float64) SkyDiffuse {
	if !(dhi > 0) {
		return SkyDiffuse{}
	}
	iso := dhi * (1 + cosd(tiltDeg)) / 2
	return SkyDiffuse{Total: iso, Isotropic: iso}
}

// haydaviesMinCosZenith bounds cos z in the beam ratio Rb (cos 89°), as in
// pvlib, so that Rb stays finite when the sun is on the horizon.
const haydaviesMinCosZenith = 0.01745

// HayDaviesSky returns the sky diffuse of Hay & Davies (1980):
//
//	Id = DHI·[A·Rb + (1 − A)·(1 + cos β)/2],  A = DNI/DNIExtra,  Rb = cos θ/cos z
//
// cosTheta is the cosine of the angle of incidence. Each component is
// clipped at zero, since A can exceed 1 with noisy inputs.
func HayDaviesSky(tiltDeg, dhi, dni, dniExtra, zenithDeg, cosTheta float64) SkyDiffuse {
	if !(dhi > 0) {
		return SkyDiffuse{}
	}
	a := dni / dniExtra
	rb := math.Max(cosTheta, 0) / math.Max(cosd(zenithDeg), haydaviesMinCosZenith)
	iso := math.Max(0, dhi*(1-a)*(1+cosd(tiltDeg))/2)
	circ := math.Max(0, dhi*a*rb)
	return SkyDiffuse{Total: iso + circ, Isotropic: iso, Circumsolar: circ}
}

// perezKappa is the constant of the sky clearness formula for zenith angles
// in radians (Perez et al., 1990).
const perezKappa = 1.041

// perezMinCosZenith bounds the denominator of the circumsolar term
// (cos 85°), as in pvlib, so that it stays finite near the horizon.
var perezMinCosZenith = cosd(85)

// perezBinUpper are the upper bounds of the sky clearness bins 1–7; bin 8 is
// open-ended (Perez et al., 1990).
var perezBinUpper = [7]float64{1.065, 1.230, 1.500, 1.950, 2.800, 4.500, 6.200}

// perezCoeff holds f11, f12, f13, f21, f22, f23 for one clearness bin.
type perezCoeff struct{ f11, f12, f13, f21, f22, f23 float64 }

// perezAllSites1990 is the "allsitescomposite1990" irradiance coefficient
// set of Perez et al. (1990), rows ordered by clearness bin.
var perezAllSites1990 = [8]perezCoeff{
	{-0.008, 0.588, -0.062, -0.060, 0.072, -0.022},
	{0.130, 0.683, -0.151, -0.019, 0.066, -0.029},
	{0.330, 0.487, -0.221, 0.055, -0.064, -0.026},
	{0.568, 0.187, -0.295, 0.109, -0.152, -0.014},
	{0.873, -0.392, -0.362, 0.226, -0.462, 0.001},
	{1.132, -1.237, -0.412, 0.288, -0.823, 0.056},
	{1.060, -1.600, -0.359, 0.264, -1.127, 0.131},
	{0.678, -0.327, -0.250, 0.156, -1.377, 0.251},
}

// PerezSky returns the sky diffuse of Perez et al. (1990), "Modeling
// daylight availability and irradiance components from direct and global
// irradiance", Solar Energy 44(5):
//
//	Id = DHI·[(1 − F1)·(1 + cos β)/2 + F1·a/b + F2·sin β]
//
// with a = max(0, cos θ), b = max(cos 85°, cos z), and the brightness
// coefficients F1, F2 from the sky clearness ε and brightness Δ. airMass is
// the relative air mass; cosTheta is the cosine of the angle of incidence.
// If the total is not positive, all components are zero.
func PerezSky(tiltDeg, dhi, dni, dniExtra, zenithDeg, airMass, cosTheta float64) SkyDiffuse {
	if !(dhi > 0) || math.IsNaN(airMass) {
		return SkyDiffuse{}
	}
	z := zenithDeg * deg2rad
	kz3 := perezKappa * z * z * z
	clearness := ((dhi+dni)/dhi + kz3) / (1 + kz3)
	brightness := dhi * airMass / dniExtra

	bin := len(perezBinUpper)
	for i, upper := range perezBinUpper {
		if clearness < upper {
			bin = i
			break
		}
	}
	c := perezAllSites1990[bin]
	f1 := math.Max(0, c.f11+c.f12*brightness+c.f13*z)
	f2 := c.f21 + c.f22*brightness + c.f23*z

	a := math.Max(0, cosTheta)
	b := math.Max(perezMinCosZenith, cosd(zenithDeg))
	iso := dhi * (1 - f1) * (1 + cosd(tiltDeg)) / 2
	circ := dhi * f1 * a / b
	hor := dhi * f2 * sind(tiltDeg)
	total := iso + circ + hor
	if !(total > 0) {
		return SkyDiffuse{}
	}
	return SkyDiffuse{Total: total, Isotropic: iso, Circumsolar: circ, Horizon: hor}
}
