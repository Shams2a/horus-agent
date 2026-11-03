package semver

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.1.0", "1.0.9", true},
		{"1.0.0", "1.0.0-rc.2", true},
		{"1.0.0-rc.10", "1.0.0-rc.2", true},
		{"v2.0.0", "1.9.9", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.0", "1.0.0", false},
		{"latest", "1.0.0", false},
		{"1.0.0", "dev", true},
		{"01.0.0", "0.1.0", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.candidate, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v", c.candidate, c.current, got)
		}
	}
}
