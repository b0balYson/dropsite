package domain

import "time"

// IPBan is a ban of a client network on site addresses (change
// abuse-protection). Key is an IPv4 address or an IPv6 /64 network;
// Addresses are the exact addresses that have a marker file for Caddy.
type IPBan struct {
	Key       string
	Level     int
	Reason    string
	Addresses []string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Active reports whether the ban is in force at now.
func (b IPBan) Active(now time.Time) bool { return now.Before(b.ExpiresAt) }
