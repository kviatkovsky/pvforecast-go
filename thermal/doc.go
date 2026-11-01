// Package thermal estimates PV cell temperature from plane-of-array
// irradiance, air temperature and wind speed.
//
// It implements the Faiman (2008) model and the Sandia Array Performance
// Model (SAPM) temperature model of King, Boyson & Kratochvil (2004,
// SAND2004-3535), including the published SAPM coefficient sets for common
// mounting configurations.
//
// Temperatures are in °C, irradiance in W/m² and wind speed in m/s.
package thermal
