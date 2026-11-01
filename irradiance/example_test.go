package irradiance_test

import (
	"fmt"

	"github.com/kviatkovsky/pvforecast-go/irradiance"
)

func ExampleTranspose() {
	// A south-facing 35° array at noon on a clear day.
	poa, err := irradiance.Transpose(irradiance.Perez, irradiance.Input{
		TiltDeg: 35, SurfaceAzimuthDeg: 180,
		ZenithDeg: 35, SolarAzimuthDeg: 180,
		GHI: 806, DNI: 850, DHI: 110,
		DNIExtra: 1361, Albedo: 0.25,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("POA %.0f W/m² = beam %.0f + sky %.0f + ground %.0f (AOI %.0f°)\n",
		poa.Global, poa.Beam, poa.Sky.Total, poa.GroundDiffuse, poa.AOIDeg)
	// Output: POA 996 W/m² = beam 850 + sky 128 + ground 18 (AOI 0°)
}

func ExampleErbs() {
	d := irradiance.Erbs(500, 50, 1361)
	fmt.Printf("DNI %.0f W/m², DHI %.0f W/m², Kt %.2f\n", d.DNI, d.DHI, d.Kt)
	// Output: DNI 387 W/m², DHI 251 W/m², Kt 0.57
}
