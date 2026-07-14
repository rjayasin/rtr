package update

import "testing"

func TestIsReleaseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"v1.2.3", true},
		{"1.2.3", true},
		{"dev", false},
		{"", false},
		{"868c814", false},           // commit hash
		{"868c814-dirty", false},     // dirty source build
		{"v1.1.3-dirty", false},      // tagged commit + local changes
		{"v1.1.3-5-gabc1234", false}, // commits after the tag (git describe)
		{"1.2.3-rc1", false},         // any suffix sorts below its own release
		{"v0.1", false},              // release tags are always three-part
		{"v1", false},
	}
	for _, tc := range cases {
		if got := isReleaseVersion(tc.in); got != tc.want {
			t.Errorf("isReleaseVersion(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
