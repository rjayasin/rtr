package util

import "testing"

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0B"},
		{1, "1B"},
		{512, "512B"},
		{1023, "1023B"},
		{1024, "1.0K"},
		{1536, "1.5K"},               // 1.5 KiB
		{1024 * 1024, "1.0M"},        // 1 MiB
		{4509715660, "4.2G"},         // ~4.2 GiB
		{1024 * 1024 * 1024, "1.0G"}, // 1 GiB
		{1 << 40, "1.0T"},            // 1 TiB
		{1 << 50, "1.0P"},            // 1 PiB
	}
	for _, tc := range cases {
		if got := HumanBytes(tc.in); got != tc.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		in   string
		want string
	}{
		{"~", home},
		{"~/sub/dir", home + "/sub/dir"},
		{"/abs/path", "/abs/path"}, // no ~ prefix: unchanged
		{"~user/x", "~user/x"},     // ~ not followed by / is not expanded
		{"", ""},
	}
	for _, tc := range cases {
		if got := ExpandHome(tc.in); got != tc.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
