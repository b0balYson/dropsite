package service

import (
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/ratelimit"
)

// Confirmation messages go to addresses typed in by users, so they are
// limited per recipient and for the whole installation: the platform must
// not become a relay for mail or Telegram messages to strangers.
const (
	FormConfirmPerRecipient    = 3
	FormConfirmRecipientWindow = 24 * time.Hour
	FormConfirmPerHour         = 100
)

type confirmLimits struct {
	recipient *ratelimit.Window
	global    *ratelimit.Window
}

func newConfirmLimits(now func() time.Time) confirmLimits {
	return confirmLimits{
		recipient: ratelimit.New(FormConfirmPerRecipient, FormConfirmRecipientWindow).WithClock(now),
		global:    ratelimit.New(FormConfirmPerHour, time.Hour).WithClock(now),
	}
}

// allow counts one confirmation message to recipient (kind-prefixed).
func (c confirmLimits) allow(recipient string) error {
	r, ok, retry := c.recipient.Reserve(strings.ToLower(recipient))
	if !ok {
		return domain.ErrRateLimited.WithRetryAfter(retry)
	}
	if _, ok, retry := c.global.Reserve("all"); !ok {
		r.Cancel()
		return domain.ErrRateLimited.WithRetryAfter(retry)
	}
	return nil
}

// selfPrefixes lists the addresses of this host, which webhooks must never
// reach even through FORM_WEBHOOK_ALLOW_CIDRS (the internal listener of
// control is bound there).
func selfPrefixes() []netip.Prefix {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		addrs = nil
	}
	return forms.SelfPrefixes(addrs)
}
