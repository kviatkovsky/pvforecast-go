package irradiance

import "math"

// Clamping conventions for the decomposition, following pvlib. They are not
// part of the Erbs model; they keep it stable near the horizon, where
// GHI/cos z amplifies measurement and forecast noise.
const (
	// ErbsMinCosZenith is the lower bound of cos z when computing the
	// clearness index (≈ 86.3°).
	ErbsMinCosZenith = 0.065
	// ErbsMaxZenith is the zenith angle, in degrees, above which all of GHI
	// is treated as diffuse.
	ErbsMaxZenith = 87.0
)

// Decomposition is the result of splitting global horizontal irradiance
// into direct and diffuse parts.
type Decomposition struct {
	DNI float64 // direct normal irradiance, W/m²
	DHI float64 // diffuse horizontal irradiance, W/m²
	Kt  float64 // clearness index, dimensionless
}

// ClearnessIndex returns the ratio of GHI to the extraterrestrial irradiance
// on a horizontal plane, with cos z bounded below by minCosZenith and the
// result clipped to [0, maxKt].
func ClearnessIndex(ghi, zenithDeg, dniExtra, minCosZenith, maxKt float64) float64 {
	horizontalExtra := dniExtra * math.Max(cosd(zenithDeg), minCosZenith)
	return math.Max(0, math.Min(maxKt, ghi/horizontalExtra))
}

// Erbs splits GHI into DNI and DHI with the diffuse-fraction correlation of
// Erbs, Klein & Duffie (1982), Solar Energy 28(4) (hourly
// correlation). Inputs are in W/m² and degrees; dniExtra is the
// extraterrestrial normal irradiance.
//
// Following pvlib: Kt is clipped to [0, 1] with cos z ≥ ErbsMinCosZenith,
// and above ErbsMaxZenith, or when the derived DNI would be negative, all of
// GHI is reported as diffuse. Unlike pvlib, negative GHI is treated as zero.
func Erbs(ghi, zenithDeg, dniExtra float64) Decomposition {
	ghi = math.Max(ghi, 0)
	kt := ClearnessIndex(ghi, zenithDeg, dniExtra, ErbsMinCosZenith, 1)

	var df float64 // diffuse fraction DHI/GHI
	switch {
	case kt <= 0.22:
		df = 1 - 0.09*kt
	case kt <= 0.80:
		df = 0.9511 + kt*(-0.1604+kt*(4.388+kt*(-16.638+kt*12.336)))
	default:
		df = 0.165
	}

	dhi := df * ghi
	dni := (ghi - dhi) / cosd(zenithDeg)
	if zenithDeg > ErbsMaxZenith || dni < 0 {
		return Decomposition{DNI: 0, DHI: ghi, Kt: kt}
	}
	return Decomposition{DNI: dni, DHI: dhi, Kt: kt}
}
