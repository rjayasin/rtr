package transfer

import "testing"

func TestParseProgressLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		ok      bool
		bytes   int64
		percent float64
		rate    string
		eta     string
	}{
		{
			name:    "comma grouped",
			line:    "         1,234,567  45%   11.22MB/s    0:00:12",
			ok:      true,
			bytes:   1234567,
			percent: 45,
			rate:    "11.22MB/s",
			eta:     "0:00:12",
		},
		{
			name:    "human readable with xfr suffix",
			line:    "          4.59M     100%  120.00MB/s    0:00:00 (xfr#1, to-chk=0/1)",
			ok:      true,
			bytes:   4812963, // 4.59 * 1048576, truncated
			percent: 100,
			rate:    "120.00MB/s",
			eta:     "0:00:00",
		},
		{
			name:    "zero start",
			line:    "              0   0%    0.00kB/s    0:00:00",
			ok:      true,
			bytes:   0,
			percent: 0,
			rate:    "0.00kB/s",
			eta:     "0:00:00",
		},
		{name: "file name line", line: "some/file.txt", ok: false},
		{name: "header line", line: "sending incremental file list", ok: false},
		{name: "blank", line: "   ", ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := ParseProgressLine(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if p.Bytes != tc.bytes {
				t.Errorf("bytes = %d, want %d", p.Bytes, tc.bytes)
			}
			if p.Percent != tc.percent {
				t.Errorf("percent = %v, want %v", p.Percent, tc.percent)
			}
			if p.Rate != tc.rate {
				t.Errorf("rate = %q, want %q", p.Rate, tc.rate)
			}
			if p.ETA != tc.eta {
				t.Errorf("eta = %q, want %q", p.ETA, tc.eta)
			}
		})
	}
}

func TestScanCRLF(t *testing.T) {
	// Mixed carriage-return progress updates and newline-terminated lines should
	// tokenize into separate samples.
	in := "a\rb\nc\r\nd"
	var got []string
	advance, start := 0, 0
	data := []byte(in)
	for start < len(data) {
		a, tok, _ := scanCRLF(data[start:], true)
		if a == 0 {
			break
		}
		got = append(got, string(tok))
		advance += a
		start += a
	}
	want := []string{"a", "b", "c", "", "d"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
