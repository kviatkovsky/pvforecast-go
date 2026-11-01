// Package power converts effective plane-of-array irradiance and cell
// temperature into DC and AC power.
//
// It implements the PVWatts Version 5 models (Dobos, 2014, NREL/TP-6A20-62641):
// the DC power model, the multiplicative system-loss model and the inverter
// part-load efficiency model with AC clipping. It also provides the ASHRAE
// and physical (Fresnel/De Soto) incidence angle modifiers applied to the
// beam component.
//
// Power is in watts and irradiance in W/m² unless a name says otherwise.
package power
