package update

import "testing"

func TestIsReleaseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"v1.2.3", true},
		{"1.2.3", true},
		{"v0.1", true},
		{"1.2.3-rc1", true}, // pre-release suffix on the patch is fine
		{"dev", false},
		{"", false},
		{"868c814", false},       // commit hash
		{"868c814-dirty", false}, // dirty source build
		{"v1", false},            // needs at least major.minor
	}
	for _, tc := range cases {
		if got := isReleaseVersion(tc.in); got != tc.want {
			t.Errorf("isReleaseVersion(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
