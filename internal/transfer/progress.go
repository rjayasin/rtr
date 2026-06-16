package transfer

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

// Progress is a single sample parsed from rsync's --info=progress2 output, which
// reports the transfer as a whole (not per-file).
type Progress struct {
	BytesRaw string  // as rsync printed it, e.g. "4.59M" or "1,234,567"
	Bytes    int64   // best-effort parse of BytesRaw
	Percent  float64 // 0..100
	Rate     string  // e.g. "11.22MB/s"
	ETA      string  // remaining time, e.g. "0:00:12"
}

// progress2 lines look like:
//
//	1,234,567  45%   11.22MB/s    0:00:12
//	4.59M     100%  120.00MB/s    0:00:00 (xfr#1, to-chk=0/1)
//
// The leading column honors -h/--human-readable, so the byte field may carry a
// K/M/G/T/P suffix or be comma-grouped digits.
var progressLine = regexp.MustCompile(
	`^\s*([0-9][0-9.,]*[KMGTP]?)\s+(\d+)%\s+(\S+)\s+(\d+:\d{2}:\d{2})`)

// ParseProgressLine extracts a Progress from one rsync output token. ok is false
// for lines that are not progress samples (file names, stats, blank lines).
func ParseProgressLine(line string) (p Progress, ok bool) {
	m := progressLine.FindStringSubmatch(line)
	if m == nil {
		return Progress{}, false
	}
	p.BytesRaw = m[1]
	p.Bytes = parseBytes(m[1])
	if pct, err := strconv.Atoi(m[2]); err == nil {
		p.Percent = float64(pct)
	}
	p.Rate = m[3]
	p.ETA = m[4]
	return p, true
}

var unitFactor = map[byte]float64{
	'K': 1 << 10, 'M': 1 << 20, 'G': 1 << 30, 'T': 1 << 40, 'P': 1 << 50,
}

func parseBytes(s string) int64 {
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0
	}
	last := s[len(s)-1]
	if f, ok := unitFactor[last]; ok {
		v, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil {
			return 0
		}
		return int64(v * f)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(v)
}

// scanCRLF is a bufio.SplitFunc that tokenizes on both '\n' and '\r' so that
// rsync's in-place progress updates (which it emits with carriage returns) are
// surfaced as individual samples rather than buffered until newline.
func scanCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil // request more data
}

var _ bufio.SplitFunc = scanCRLF
