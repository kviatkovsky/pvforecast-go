package solarpos

import (
	"fmt"
	"math"
	"time"
)

// DefaultDeltaT is the ΔT = TT − UT, in seconds, used when SPA.DeltaT is
// zero. It matches the pvlib default. ΔT enters only the ephemeris (not the
// hour angle), so an error of a few seconds changes the solar angles by far
// less than 0.001°.
const DefaultDeltaT = 67.0

// SPA is the NREL Solar Position Algorithm (Reda & Andreas, 2004,
// NREL/TP-560-34302), with a stated uncertainty of ±0.0003° in zenith and
// azimuth for the years -2000 to 6000.
//
// The zero value is ready to use. Equation numbers in the implementation
// refer to Section 3 of the report.
type SPA struct {
	// DeltaT is TT − UT in seconds, in [-8000, 8000]. Zero selects
	// DefaultDeltaT.
	DeltaT float64
}

var _ Algorithm = SPA{}

// Position implements Algorithm.
func (a SPA) Position(t time.Time, site Site) (Position, error) {
	if err := site.Validate(); err != nil {
		return Position{}, err
	}
	jd, err := checkTime(t)
	if err != nil {
		return Position{}, err
	}
	deltaT := a.DeltaT
	if deltaT == 0 {
		deltaT = DefaultDeltaT
	}
	if !inRange(deltaT, -8000, 8000) {
		return Position{}, fmt.Errorf("%w: ΔT %g s outside [-8000, 8000]", ErrOutOfRange, deltaT)
	}
	pressureHPa, tempC := site.conditions()
	return spa(jd, deltaT, site.Lat, site.Lon, site.AltitudeM, pressureHPa, tempC), nil
}

// spa evaluates the algorithm for a validated input. Angles are in degrees
// unless a name ends in R (radians).
func spa(jd, deltaT, lat, lon, altM, pressureHPa, tempC float64) Position {
	// 3.1 Julian ephemeris day, century and millennium (eq. 5–9).
	jde := jd + deltaT/86400
	jc := (jd - 2451545) / 36525
	jce := (jde - 2451545) / 36525
	jme := jce / 10

	// 3.2 Earth heliocentric longitude, latitude and radius vector
	// (eq. 9–14).
	l := limitDegrees(seriesSum(spaL[:], jme) * rad2deg)
	b := seriesSum(spaB[:], jme) * rad2deg
	r := seriesSum(spaR[:], jme)

	// 3.3 Geocentric longitude and latitude (eq. 13–14).
	theta := limitDegrees(l + 180)
	beta := -b

	// 3.4 Nutation in longitude and obliquity (eq. 15–23).
	dPsi, dEps := nutation(jce)

	// 3.5 True obliquity of the ecliptic (eq. 24–25).
	eps := meanObliquity(jme/10)/3600 + dEps

	// 3.6–3.7 Aberration correction and apparent sun longitude (eq. 26–27).
	dTau := -20.4898 / (3600 * r)
	lambda := theta + dPsi + dTau

	// 3.8 Apparent sidereal time at Greenwich (eq. 28–29).
	nu0 := limitDegrees(280.46061837 + 360.98564736629*(jd-2451545) +
		0.000387933*jc*jc - jc*jc*jc/38710000)
	epsR := eps * deg2rad
	nu := nu0 + dPsi*math.Cos(epsR)

	// 3.9 Geocentric sun right ascension and declination (eq. 30–31).
	lambdaR, betaR := lambda*deg2rad, beta*deg2rad
	alpha := limitDegrees(rad2deg * math.Atan2(
		math.Sin(lambdaR)*math.Cos(epsR)-math.Tan(betaR)*math.Sin(epsR),
		math.Cos(lambdaR)))
	deltaR := math.Asin(math.Sin(betaR)*math.Cos(epsR) +
		math.Cos(betaR)*math.Sin(epsR)*math.Sin(lambdaR))

	// 3.10 Observer local hour angle (eq. 32).
	hR := limitDegrees(nu+lon-alpha) * deg2rad

	// 3.11 Topocentric sun right ascension and declination (eq. 33–38).
	latR := lat * deg2rad
	xiR := 8.794 / (3600 * r) * deg2rad
	uR := math.Atan(0.99664719 * math.Tan(latR))
	x := math.Cos(uR) + altM/6378140*math.Cos(latR)
	y := 0.99664719*math.Sin(uR) + altM/6378140*math.Sin(latR)
	dAlphaR := math.Atan2(
		-x*math.Sin(xiR)*math.Sin(hR),
		math.Cos(deltaR)-x*math.Sin(xiR)*math.Cos(hR))
	deltaPrimeR := math.Atan2(
		(math.Sin(deltaR)-y*math.Sin(xiR))*math.Cos(dAlphaR),
		math.Cos(deltaR)-x*math.Sin(xiR)*math.Cos(hR))

	// 3.12 Topocentric local hour angle (eq. 39).
	hPrimeR := hR - dAlphaR

	// 3.13 Topocentric zenith angle (eq. 40–44).
	e0 := rad2deg * math.Asin(math.Sin(latR)*math.Sin(deltaPrimeR)+
		math.Cos(latR)*math.Cos(deltaPrimeR)*math.Cos(hPrimeR))
	e := e0 + refraction(e0, pressureHPa, tempC)

	// 3.14 Topocentric azimuth, measured eastward from north (eq. 45–46).
	gamma := rad2deg * math.Atan2(math.Sin(hPrimeR),
		math.Cos(hPrimeR)*math.Sin(latR)-math.Tan(deltaPrimeR)*math.Cos(latR))
	azimuth := limitDegrees(gamma + 180)

	return Position{
		Zenith:            90 - e0,
		ApparentZenith:    90 - e,
		Elevation:         e0,
		ApparentElevation: e,
		Azimuth:           azimuth,
		EarthSunDistance:  r,
		DNIExtra:          SolarConstant / (r * r),
	}
}

// seriesSum evaluates Σ_i (Σ_j A_ij·cos(B_ij + C_ij·τ))·τ^i / 10⁸ (eq. 9–11),
// the common form of the heliocentric longitude, latitude and radius vector.
func seriesSum(groups [][]spaTerm, tau float64) float64 {
	var sum, pow float64 = 0, 1
	for _, terms := range groups {
		var s float64
		for _, t := range terms {
			s += t.a * math.Cos(t.b+t.c*tau)
		}
		sum += s * pow
		pow *= tau
	}
	return sum / 1e8
}

// nutation returns the nutation in longitude and obliquity, in degrees
// (eq. 15–23).
func nutation(jce float64) (dPsi, dEps float64) {
	poly := func(a, b, c, d float64) float64 {
		return a + jce*(b+jce*(c+jce/d))
	}
	// Mean elongation of the moon, mean anomalies of the sun and moon, the
	// moon's argument of latitude and the longitude of its ascending node.
	xs := [5]float64{
		poly(297.85036, 445267.111480, -0.0019142, 189474),
		poly(357.52772, 35999.050340, -0.0001603, -300000),
		poly(134.96298, 477198.867398, 0.0086972, 56250),
		poly(93.27191, 483202.017538, -0.0036825, 327270),
		poly(125.04452, -1934.136261, 0.0020708, 450000),
	}
	for i := range spaNutation {
		n := &spaNutation[i]
		var arg float64
		for j, x := range xs {
			arg += x * n.y[j]
		}
		argR := arg * deg2rad
		dPsi += (n.a + n.b*jce) * math.Sin(argR)
		dEps += (n.c + n.d*jce) * math.Cos(argR)
	}
	// Table A4.3 coefficients are in units of 0.0001 arcseconds.
	return dPsi / 36e6, dEps / 36e6
}

// meanObliquity returns the mean obliquity of the ecliptic in arcseconds for
// U = JME/10 (eq. 24).
func meanObliquity(u float64) float64 {
	coeffs := [...]float64{
		84381.448, -4680.93, -1.55, 1999.25, -51.38, -249.67,
		-39.05, 7.12, 27.87, 5.79, 2.45,
	}
	var sum float64
	for i := len(coeffs) - 1; i >= 0; i-- {
		sum = sum*u + coeffs[i]
	}
	return sum
}

// refraction returns the atmospheric refraction correction in degrees for a
// geometric elevation e0 in degrees (eq. 42). Following the reference
// implementation, no correction is applied once the upper limb of the Sun is
// geometrically below the refracted horizon.
func refraction(e0, pressureHPa, tempC float64) float64 {
	const (
		sunRadius        = 0.26667 // degrees
		horizonRefract   = 0.5667  // refraction at the horizon, degrees
		belowHorizonEdge = -(sunRadius + horizonRefract)
	)
	if e0 < belowHorizonEdge {
		return 0
	}
	return pressureHPa / 1010 * 283 / (273 + tempC) *
		1.02 / (60 * math.Tan((e0+10.3/(e0+5.11))*deg2rad))
}
