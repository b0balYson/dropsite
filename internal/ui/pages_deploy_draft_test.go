package ui

import "testing"

func TestIsTruthy(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0": false, "false": false, "OFF": false, " no ": false,
		"1": true, "true": true, "on": true, "yes": true} {
		if got := isTruthy(v); got != want {
			t.Errorf("isTruthy(%q) = %v, want %v", v, got, want)
		}
	}
}
