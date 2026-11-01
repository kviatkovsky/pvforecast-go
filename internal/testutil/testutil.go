// Package testutil provides helpers shared by the test suites: loading the
// pvlib reference fixtures and comparing floating-point values with explicit
// tolerances.
package testutil

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// TestdataPath returns the absolute path of a file in internal/testdata.
func TestdataPath(name string) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("testutil: cannot determine source location")
	}
	return filepath.Join(filepath.Dir(file), "..", "testdata", name)
}

// Table is a CSV fixture with a header row.
type Table struct {
	cols map[string]int
	Rows [][]string
}

// LoadCSV reads a CSV fixture from internal/testdata. It fails the test if
// the file cannot be read or is empty.
func LoadCSV(tb testing.TB, name string) *Table {
	tb.Helper()
	f, err := os.Open(TestdataPath(name))
	if err != nil {
		tb.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		tb.Fatalf("read fixture %s: %v", name, err)
	}
	if len(records) < 2 {
		tb.Fatalf("fixture %s has no data rows", name)
	}
	cols := make(map[string]int, len(records[0]))
	for i, c := range records[0] {
		cols[c] = i
	}
	return &Table{cols: cols, Rows: records[1:]}
}

// String returns the value of column col in row i.
func (t *Table) String(tb testing.TB, i int, col string) string {
	tb.Helper()
	j, ok := t.cols[col]
	if !ok {
		tb.Fatalf("fixture has no column %q", col)
	}
	return t.Rows[i][j]
}

// Float returns the value of column col in row i parsed as a float64.
func (t *Table) Float(tb testing.TB, i int, col string) float64 {
	tb.Helper()
	v, err := strconv.ParseFloat(t.String(tb, i, col), 64)
	if err != nil {
		tb.Fatalf("row %d column %q: %v", i, col, err)
	}
	return v
}

// MaxErr tracks the largest absolute error seen for one quantity.
type MaxErr struct {
	Name  string
	Tol   float64
	Max   float64
	Where string
	Count int
}

// Observe records |got − want| and reports whether it is within tolerance.
// NaN in either value counts as out of tolerance.
func (m *MaxErr) Observe(got, want float64, where string) bool {
	return m.ObserveDiff(got-want, where)
}

// ObserveDiff records an already computed difference, for quantities such
// as angles that need a custom difference.
func (m *MaxErr) ObserveDiff(diff float64, where string) bool {
	m.Count++
	d := math.Abs(diff)
	if math.IsNaN(d) {
		d = math.Inf(1)
	}
	if d > m.Max {
		m.Max, m.Where = d, where
	}
	return d <= m.Tol
}

// Report logs the maximum error and fails the test if it exceeds the
// tolerance.
func (m *MaxErr) Report(tb testing.TB) {
	tb.Helper()
	tb.Logf("%-20s max |err| = %.3g (tol %.3g) over %d samples, at %s", m.Name, m.Max, m.Tol, m.Count, m.Where)
	if m.Max > m.Tol {
		tb.Errorf("%s: max |err| %.3g exceeds tolerance %.3g at %s", m.Name, m.Max, m.Tol, m.Where)
	}
}
