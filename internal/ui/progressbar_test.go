package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/rjayasin/rtr/internal/transfer"
)

// The bar occupies exactly the width it is given, at every percentage.
func TestRenderBarWidth(t *testing.T) {
	for _, w := range []int{20, 30, 40} {
		for _, p := range []float64{-5, 0, 0.4, 33.3, 99.9, 100, 140} {
			if got := ansi.StringWidth(renderBar(w, p)); got != w {
				t.Errorf("renderBar(%d, %v) width = %d, want %d", w, p, got, w)
			}
		}
	}
}

// Sub-percent progress moves the fill: a partial-block cell means fractions of
// a percent are visible, and every 1% step changes the rendering even on a bar
// far narrower than 100 cells.
func TestRenderBarSubCellResolution(t *testing.T) {
	// Half a cell of a 20-wide bar (13 cells) is under 4% — a whole-cell bar
	// would render it identically to 0%.
	if got := renderBar(20, 3.8); !strings.ContainsAny(got, string(barEighths[1:])) {
		t.Errorf("renderBar(20, 3.8) = %q, want a partial block", got)
	}

	prev := renderBar(20, 0)
	for pct := 1; pct <= 100; pct++ {
		got := renderBar(20, float64(pct))
		if got == prev {
			t.Fatalf("renderBar(20, %d) is indistinguishable from %d%%", pct, pct-1)
		}
		prev = got
	}
}

// applyProgress refines rsync's whole percents with the byte counter: inside a
// percent the bar advances, but never past the next whole percent rsync has yet
// to report.
func TestApplyProgressInterpolates(t *testing.T) {
	x := &xfer{}
	// 10% of a 1000-byte transfer: the sample itself sets the scale
	// (100 bytes = 10%, so 10 bytes per percent).
	x.applyProgress(transfer.Progress{Percent: 10, Bytes: 100, Rate: "1MB/s", ETA: "0:00:09"})
	if x.pct != 10 {
		t.Fatalf("pct = %v at the start of the bracket, want 10", x.pct)
	}
	if x.rate != "1MB/s" || x.eta != "0:00:09" || x.bytes != 100 {
		t.Fatalf("sample fields not applied: %+v", x)
	}

	// Half-way into the 10% bracket, with rsync still reporting 10%.
	x.applyProgress(transfer.Progress{Percent: 10, Bytes: 105})
	if x.pct <= 10.4 || x.pct >= 10.6 {
		t.Errorf("pct = %v mid-bracket, want ~10.5", x.pct)
	}

	// More bytes than the estimate expected: the display stays inside the
	// bracket rather than claiming a percent rsync has not reported.
	x.applyProgress(transfer.Progress{Percent: 10, Bytes: 400})
	if x.pct < 10 || x.pct >= 11 {
		t.Errorf("pct = %v, want clamped to [10, 11)", x.pct)
	}
}

// A resumed transfer restarts its byte count from a lower value; the
// interpolation re-anchors instead of reporting a negative offset.
func TestApplyProgressRestart(t *testing.T) {
	x := &xfer{}
	x.applyProgress(transfer.Progress{Percent: 40, Bytes: 4000})
	x.applyProgress(transfer.Progress{Percent: 40, Bytes: 30})
	if x.pct != 40 {
		t.Errorf("pct = %v after a restart, want 40", x.pct)
	}
	x.applyProgress(transfer.Progress{Percent: 40, Bytes: 60})
	if x.pct <= 40 || x.pct >= 41 {
		t.Errorf("pct = %v after re-anchoring, want (40, 41)", x.pct)
	}
}

// Before rsync reports its first percent there is nothing to interpolate
// against, so the size the popover measured seeds the scale.
func TestApplyProgressSeededBySizeWalk(t *testing.T) {
	m := testModel()
	m.pendingSize = 100_000
	if got := m.sizeScale(); got != 1000 {
		t.Fatalf("sizeScale = %v, want 1000 bytes per percent", got)
	}
	m.sizeLoading = true
	if got := m.sizeScale(); got != 0 {
		t.Fatalf("sizeScale = %v while the walk is running, want 0", got)
	}

	x := &xfer{bytesPerPct: 1000}
	x.applyProgress(transfer.Progress{Percent: 0, Bytes: 500})
	if x.pct <= 0 || x.pct >= 1 {
		t.Errorf("pct = %v below rsync's first percent, want (0, 1)", x.pct)
	}
}
