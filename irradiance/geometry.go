package irradiance

import "math"

const (
	deg2rad = math.Pi / 180
	rad2deg = 180 / math.Pi
)

func cosd(d float64) float64 { return math.Cos(d * deg2rad) }
func sind(d float64) float64 { return math.Sin(d * deg2rad) }

// AOI returns the angle of incidence, in degrees, between the sun direction
// and the normal of a surface with the given tilt (0° = horizontal) and
// azimuth (clockwise from north). Solar zenith and azimuth are in degrees.
//
// The result is in [0, 180]; values above 90° mean the sun is behind the
// surface.
func AOI(tiltDeg, surfaceAzimuthDeg, zenithDeg, solarAzimuthDeg float64) float64 {
	return rad2deg * math.Acos(cosAOI(tiltDeg, surfaceAzimuthDeg, zenithDeg, solarAzimuthDeg))
}

// cosAOI is the dot product of the sun vector and the surface normal
// (Duffie & Beckman, 2013, eq. 1.6.3 in zenith/azimuth form), clipped to
// [-1, 1] to absorb rounding.
func cosAOI(tiltDeg, surfaceAzimuthDeg, zenithDeg, solarAzimuthDeg float64) float64 {
	c := cosd(tiltDeg)*cosd(zenithDeg) +
		sind(tiltDeg)*sind(zenithDeg)*cosd(solarAzimuthDeg-surfaceAzimuthDeg)
	return math.Max(-1, math.Min(1, c))
}

// RelativeAirMass returns the relative optical air mass for an apparent
// solar zenith angle in degrees, using Kasten & Young (1989), "Revised
// optical air mass tables and approximation formula", Applied Optics 28(22):
//
//	AM = 1 / (cos z + 0.50572·(96.07995° − z)^−1.6364)
//
// It returns NaN for z > 90°, where the sun is below the horizon and air
// mass is undefined.
func RelativeAirMass(zenithDeg float64) float64 {
	if !(zenithDeg <= 90) {
		return math.NaN()
	}
	return 1 / (cosd(zenithDeg) + 0.50572*math.Pow(96.07995-zenithDeg, -1.6364))
}
