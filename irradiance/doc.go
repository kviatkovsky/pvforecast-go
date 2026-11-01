// Package irradiance converts horizontal irradiance into plane-of-array (POA)
// irradiance for a fixed-tilt surface.
//
// It provides the angle of incidence, the sky-diffuse transposition models
// isotropic, Hay & Davies (1980) and Perez et al. (1990), the ground-reflected
// component, relative air mass (Kasten & Young, 1989) and the Erbs et al.
// (1982) decomposition of global horizontal irradiance into its direct and
// diffuse parts.
//
// All irradiance values are in W/m². Angles are in degrees at the API
// boundary.
package irradiance
