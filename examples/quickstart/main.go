// Command quickstart prints a three-day energy forecast for an east-west
// rooftop system in Kraków. It needs network access to Open-Meteo.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/kviatkovsky/pvforecast-go/forecast"
	"github.com/kviatkovsky/pvforecast-go/power"
	"github.com/kviatkovsky/pvforecast-go/weather/openmeteo"
)

func main() {
	sys := forecast.System{
		Name: "Warehouse", Lat: 50.0647, Lon: 19.9450, AltitudeM: 219,
		Arrays: []forecast.Array{
			{Name: "east", KWp: 24, TiltDeg: 12, AzimuthDeg: 90},
			{Name: "west", KWp: 24, TiltDeg: 12, AzimuthDeg: 270},
		},
		Inverter: power.Inverter{RatedACW: 40_000},
		Losses:   power.DefaultLosses,
	}
	opt := forecast.DefaultOptions() // Perez + SPA
	opt.Location, _ = time.LoadLocation("Europe/Warsaw")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	res, err := forecast.Run(ctx, openmeteo.New(), sys, 3, opt)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range res.Daily {
		fmt.Printf("%s  %.1f kWh\n", d.Date, d.KWh)
	}
}
