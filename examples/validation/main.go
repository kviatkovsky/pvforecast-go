// Command validation compares pvforecast-go with PVGIS (EU JRC) hourly
// data for one year at one site.
//
// PVGIS models PV output itself (satellite irradiance, its own module and
// temperature models), so this is a cross-model comparison, not a
// comparison with measurements. It shows how far two independent,
// published modeling chains agree when fed the same irradiance.
//
// Two PVGIS seriescalc responses are needed: the horizontal irradiance
// components (angle=0) to drive this library, and the PV output of the
// tilted reference system to compare against. Fetch them with -fetch, or
// pass files saved earlier:
//
//	go run ./examples/validation -fetch -out examples/validation/data
//	go run ./examples/validation -horizontal data/horizontal.json -tilted data/tilted.json
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kviatkovsky/pvforecast-go/forecast"
	"github.com/kviatkovsky/pvforecast-go/power"
)

// Reference system, matching the PVGIS request below.
const (
	lat, lon  = 50.0647, 19.9450
	altitudeM = 219
	tiltDeg   = 35
	lossPct   = 14
	year      = 2020
)

func main() { os.Exit(run()) }

func run() int {
	var (
		fetch      = flag.Bool("fetch", false, "download both PVGIS responses before comparing")
		out        = flag.String("out", "examples/validation/data", "directory for downloaded responses")
		horizontal = flag.String("horizontal", "", "PVGIS seriescalc JSON with horizontal components (angle=0)")
		tilted     = flag.String("tilted", "", "PVGIS seriescalc JSON with PV output for the tilted system")
	)
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if *fetch {
		var err error
		if *horizontal, *tilted, err = download(ctx, *out); err != nil {
			logger.Error("download", "err", err)
			return 1
		}
	}
	if *horizontal == "" || *tilted == "" {
		flag.Usage()
		return 2
	}
	report, err := compare(*horizontal, *tilted)
	if err != nil {
		logger.Error("compare", "err", err)
		return 1
	}
	fmt.Print(report)
	return 0
}

func compare(horizontalPath, tiltedPath string) (string, error) {
	h, err := readHourly(horizontalPath)
	if err != nil {
		return "", err
	}
	t, err := readHourly(tiltedPath)
	if err != nil {
		return "", err
	}
	series, err := toSeries(h)
	if err != nil {
		return "", err
	}
	sys := forecast.System{
		Name: "PVGIS reference", Lat: lat, Lon: lon, AltitudeM: altitudeM,
		Arrays: []forecast.Array{{KWp: 1, TiltDeg: tiltDeg, AzimuthDeg: 180}},
		// PVGIS reports PV output after its system losses and has no
		// separate inverter model, so the comparison uses DC power after
		// losses. The inverter only has to be valid; it never clips here.
		Inverter: power.Inverter{RatedACW: 10_000},
		Losses:   power.Losses{Other: lossPct},
	}
	res, err := forecast.Compute(sys, series, forecast.DefaultOptions())
	if err != nil {
		return "", err
	}
	return summarize(res, t)
}

func download(ctx context.Context, dir string) (horizontalPath, tiltedPath string, err error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", err
	}
	common := url.Values{
		"lat": {strconv.FormatFloat(lat, 'f', -1, 64)}, "lon": {strconv.FormatFloat(lon, 'f', -1, 64)},
		"startyear": {strconv.Itoa(year)}, "endyear": {strconv.Itoa(year)},
		"outputformat": {"json"}, "components": {"1"}, "aspect": {"0"}, "usehorizon": {"0"},
	}
	horizontal := cloneWith(common, "angle", "0")
	tilted := cloneWith(common, "angle", strconv.Itoa(tiltDeg))
	tilted.Set("pvcalculation", "1")
	tilted.Set("peakpower", "1")
	tilted.Set("loss", strconv.Itoa(lossPct))

	horizontalPath = filepath.Join(dir, "horizontal.json")
	tiltedPath = filepath.Join(dir, "tilted.json")
	for path, q := range map[string]url.Values{horizontalPath: horizontal, tiltedPath: tilted} {
		if err := get(ctx, "https://re.jrc.ec.europa.eu/api/v5_3/seriescalc?"+q.Encode(), path); err != nil {
			return "", "", err
		}
	}
	return horizontalPath, tiltedPath, nil
}

func cloneWith(v url.Values, key, value string) url.Values {
	c := url.Values{}
	for k, vs := range v {
		c[k] = append([]string(nil), vs...)
	}
	c.Set(key, value)
	return c
}

func get(ctx context.Context, u, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("pvgis: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("pvgis: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return errors.New("pvgis: HTTP " + resp.Status + ": " + string(body))
	}
	return os.WriteFile(path, body, 0o600)
}
