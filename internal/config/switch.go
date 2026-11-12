package config

import "strings"

// onOff reads an on/off switch (on, off, true, false, 1, 0, yes, no).
func (r *reader) onOff(key string, def bool) bool {
	raw := strings.ToLower(r.str(key, ""))
	switch raw {
	case "":
		return def
	case "on", "true", "1", "yes":
		return true
	case "off", "false", "0", "no":
		return false
	}
	r.fail(key, "must be on or off, got %q", raw)
	return def
}
