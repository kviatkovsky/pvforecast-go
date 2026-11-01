package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kviatkovsky/pvforecast-go/internal/testutil"
	"github.com/kviatkovsky/pvforecast-go/weather"
	"github.com/kviatkovsky/pvforecast-go/weather/openmeteo"
)

var update = flag.Bool("update", false, "rewrite golden files")

var fixtureNow = time.Date(2026, time.June, 20, 0, 0, 0, 0, time.UTC)

// fixtureDeps serves the synthetic Open-Meteo response through the real
// client with an in-memory transport and a fixed clock.
func fixtureDeps(t *testing.T) deps {
	t.Helper()
	body, err := os.ReadFile(testutil.TestdataPath("openmeteo/forecast_averaged.json"))
	if err != nil {
		t.Fatal(err)
	}
	return deps{
		provider: openmeteo.New(
			openmeteo.WithHTTPClient(staticClient(body)),
			openmeteo.WithClock(func() time.Time { return fixtureNow }),
		),
		now: func() time.Time { return fixtureNow },
	}
}

func runCLI(t *testing.T, d deps, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(t.Context(), args, &out, &errOut, d)
	return code, out.String(), errOut.String()
}

func TestGolden(t *testing.T) {
	t.Parallel()
	base := []string{"--lat", "50.0647", "--lon", "19.945", "--altitude", "219", "--kwp", "10", "--days", "2", "--tz", "Europe/Warsaw"}
	tests := []struct {
		name string
		args []string
	}{
		{"table", base},
		{"json", append(base, "--format", "json")},
		{"csv", append(base, "--format", "csv")},
		{"config_table", []string{"--config", "../../examples/system.json", "--days", "2", "--tz", "Europe/Warsaw"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runCLI(t, fixtureDeps(t), tt.args...)
			if code != exitOK {
				t.Fatalf("exit %d, stderr: %s", code, stderr)
			}
			golden := filepath.Join("testdata", tt.name+".golden")
			if *update {
				if err := os.WriteFile(golden, []byte(stdout), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test -update to create it)", err)
			}
			if stdout != string(want) {
				t.Errorf("output differs from %s (run go test -update after checking):\n%s", golden, stdout)
			}
		})
	}
}

func TestExitCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		provider weather.Provider
		want     int
		stderr   string
	}{
		{"help", []string{"--help"}, nil, exitOK, "Usage:"},
		{"unknown flag", []string{"--bogus"}, nil, exitUsage, "flag provided but not defined"},
		{"missing kwp", []string{"--lat", "50", "--lon", "20"}, nil, exitUsage, "--kwp is required"},
		{"bad model", []string{"--lat", "50", "--lon", "20", "--kwp", "5", "--model", "klucher"}, nil, exitUsage, "unknown transposition model"},
		{"bad format", []string{"--lat", "50", "--lon", "20", "--kwp", "5", "--format", "xml"}, nil, exitUsage, "--format"},
		{"bad tz", []string{"--lat", "50", "--lon", "20", "--kwp", "5", "--tz", "Mars/Olympus"}, nil, exitUsage, "--tz"},
		{"bad days", []string{"--lat", "50", "--lon", "20", "--kwp", "5", "--days", "9"}, nil, exitUsage, "--days"},
		{"bad solarpos", []string{"--lat", "50", "--lon", "20", "--kwp", "5", "--solarpos", "ephem"}, nil, exitUsage, "--solarpos"},
		{"bad dcac", []string{"--lat", "50", "--lon", "20", "--kwp", "5", "--dcac", "0"}, nil, exitUsage, "--dcac"},
		{"bad log level", []string{"--log-level", "loud"}, nil, exitUsage, "--log-level"},
		{"stray argument", []string{"forecast"}, nil, exitUsage, "unexpected argument"},
		{"invalid system", []string{"--lat", "95", "--lon", "20", "--kwp", "5"}, nil, exitUsage, "lat: 95 is outside"},
		{"config with system flags", []string{"--config", "../../examples/system.json", "--kwp", "5"}, nil, exitUsage, "cannot be combined"},
		{"missing config file", []string{"--config", "nope.json"}, nil, exitUsage, "nope.json"},
		{
			"weather service down",
			[]string{"--lat", "50", "--lon", "20", "--kwp", "5"},
			failingProvider{weather.ErrUpstream},
			exitRuntime, "upstream failure",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := fixtureDeps(t)
			if tt.provider != nil {
				d.provider = tt.provider
			}
			code, _, stderr := runCLI(t, d, tt.args...)
			if code != tt.want {
				t.Errorf("exit code %d, want %d (stderr: %s)", code, tt.want, stderr)
			}
			if !strings.Contains(stderr, tt.stderr) {
				t.Errorf("stderr %q does not contain %q", stderr, tt.stderr)
			}
		})
	}
}

type failingProvider struct{ err error }

func (f failingProvider) Forecast(context.Context, weather.Request) (weather.Series, error) {
	return weather.Series{}, f.err
}

func TestVersion(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runCLI(t, deps{}, "version")
	if code != exitOK || !strings.HasPrefix(stdout, "pvforecast ") {
		t.Errorf("version: exit %d, output %q", code, stdout)
	}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{"unknown field", `{"lat":1,"lon":2,"tilt_deg":3}`, "unknown field"},
		{"missing location", `{"arrays":[{"kwp":1}]}`, "lat and lon are required"},
		{"both loss forms", `{"lat":1,"lon":2,"losses":{},"losses_pct":10}`, "either losses or losses_pct"},
		{"both inverter forms", `{"lat":1,"lon":2,"arrays":[{"kwp":1}],"inverter":{"ac_kw":1,"dc_ac_ratio":1.2}}`, "either ac_kw or dc_ac_ratio"},
		{"unknown thermal model", `{"lat":1,"lon":2,"arrays":[{"kwp":1,"thermal":"noct"}]}`, "unknown model"},
		{"unknown iam", `{"lat":1,"lon":2,"arrays":[{"kwp":1,"iam":"martin"}]}`, "unknown IAM"},
		{"unknown sky model", `{"lat":1,"lon":2,"model":"klucher"}`, "unknown transposition model"},
		{"valid single total", `{"lat":1,"lon":2,"arrays":[{"kwp":4,"iam":"ashrae"}],"inverter":{"ac_kw":3},"losses_pct":12}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := decodeConfig(strings.NewReader(tt.json))
			if err == nil {
				_, err = cfg.system(1.1)
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
	cfg, err := loadConfig("../../examples/system.json")
	if err != nil {
		t.Fatal(err)
	}
	sys, err := cfg.system(1.1)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Validate(); err != nil {
		t.Errorf("examples/system.json is invalid: %v", err)
	}
	if got := sys.Inverter.RatedACW; got != 40000 {
		t.Errorf("inverter rating %g W, want 48 kWp / 1.2 = 40000 W", got)
	}
}

func TestRuntimeErrorIsNotUsage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stderr bytes.Buffer
	code := run(ctx, []string{"--lat", "50", "--lon", "20", "--kwp", "5"}, &bytes.Buffer{}, &stderr,
		deps{provider: failingProvider{context.Canceled}, now: time.Now})
	if code != exitRuntime {
		t.Errorf("cancellation: exit %d, want %d (stderr %s)", code, exitRuntime, stderr.String())
	}
}
