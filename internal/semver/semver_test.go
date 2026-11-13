package semver

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"1.2.3", "1.2.3", true},
		{"v1.2.3", "1.2.3", true},
		{"0.10.0-rc.1", "0.10.0-rc.1", true},
		{"1.2.3+build.5", "1.2.3", true},
		{" 2.0.0 ", "2.0.0", true},
		{"dev", "", false},
		{"dev+abc1234", "", false},
		{"dev+abc1234.dirty", "", false},
		{"latest", "", false},
		{"1.2", "", false},
		{"1.2.3.4", "", false},
		{"01.2.3", "", false},
		{"1.+2.3", "", false},
		{"1.-2.3", "", false},
		{"1.2.3-", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		v, ok := Parse(tc.in)
		if ok != tc.ok || (ok && v.String() != tc.want) {
			t.Errorf("Parse(%q) = %q, %v; want %q, %v", tc.in, v.String(), ok, tc.want, tc.ok)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.3.0", "1.2.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.1", "1.0.0-rc.2", -1},
	}
	for _, tc := range tests {
		a, _ := Parse(tc.a)
		b, _ := Parse(tc.b)
		if got := Compare(a, b); got != tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMinorApart(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.2.3", "1.2.9", false},
		{"1.2.3", "1.2.3-rc.1", false},
		{"1.2.3", "1.3.0", true},
		{"1.2.3", "2.2.3", true},
	}
	for _, tc := range tests {
		a, _ := Parse(tc.a)
		b, _ := Parse(tc.b)
		if got := MinorApart(a, b); got != tc.want {
			t.Errorf("MinorApart(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
