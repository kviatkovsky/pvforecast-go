package power

import (
	"fmt"
	"math"
)

// PVWatts v5 inverter defaults (Dobos, 2014).
const (
	DefaultEtaNom = 0.96   // nominal efficiency
	DefaultEtaRef = 0.9637 // reference efficiency of the CEC curve
)

// Inverter is the PVWatts v5 inverter model: a part-load efficiency curve
// and clipping at the AC rating.
//
// The zero values of EtaNom and EtaRef select the PVWatts defaults.
type Inverter struct {
	// RatedACW is the maximum AC output in watts (Pac0).
	RatedACW float64
	EtaNom   float64
	EtaRef   float64
}

func (inv Inverter) etas() (nom, ref float64) {
	nom, ref = inv.EtaNom, inv.EtaRef
	if nom == 0 {
		nom = DefaultEtaNom
	}
	if ref == 0 {
		ref = DefaultEtaRef
	}
	return nom, ref
}

// Validate reports whether the rating is positive and the efficiencies are
// in (0, 1].
func (inv Inverter) Validate() error {
	nom, ref := inv.etas()
	switch {
	case !(inv.RatedACW > 0) || math.IsInf(inv.RatedACW, 0):
		return fmt.Errorf("%w: inverter AC rating %g W must be positive", ErrInvalidParameters, inv.RatedACW)
	case !(nom > 0 && nom <= 1), !(ref > 0 && ref <= 1):
		return fmt.Errorf("%w: inverter efficiencies %g, %g outside (0, 1]", ErrInvalidParameters, nom, ref)
	}
	return nil
}

// AC returns the AC output in watts for a DC input in watts, and whether
// the output was clipped at RatedACW (Dobos, 2014):
//
//	ζ = Pdc/Pdc0,  Pdc0 = Pac0/ηnom
//	η = (ηnom/ηref)·(−0.0162·ζ − 0.0059/ζ + 0.9858)
//	Pac = min(η·Pdc, Pac0)
//
// At very low input the curve gives a negative efficiency, which models the
// inverter's own consumption; the output is then zero.
func (inv Inverter) AC(pdcW float64) (pacW float64, clipped bool) {
	if !(pdcW > 0) {
		return 0, false
	}
	nom, ref := inv.etas()
	pdc0 := inv.RatedACW / nom
	zeta := pdcW / pdc0
	eta := nom / ref * (-0.0162*zeta - 0.0059/zeta + 0.9858)
	pac := eta * pdcW
	if pac > inv.RatedACW {
		return inv.RatedACW, true
	}
	return math.Max(0, pac), false
}
