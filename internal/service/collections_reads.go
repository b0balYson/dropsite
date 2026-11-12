package service

import (
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
)

// Visitor reads of /_dropsite/data are public and cheap to send, so they
// have their own per-IP limit and a smaller page than the owner API.
const (
	// DataReadsPerIP allows about 60 page views a minute from one address
	// (an office behind NAT): a page usually loads 1-5 collections.
	DataReadsPerIP      = 300
	DataReadsPerIPWindow = time.Minute
	// MaxPublicDocumentPage caps a visitor page (about 3.2 MB at 16 KB per
	// document). A larger limit, still accepted up to MaxDocumentPage for
	// pages written earlier, is cut to this size and continues with next.
	MaxPublicDocumentPage = 200
)

func newDataReadLimit(now func() time.Time) *ratelimit.Window {
	return ratelimit.New(DataReadsPerIP, DataReadsPerIPWindow).WithClock(now)
}

// publicPageLimit cuts a visitor page size to MaxPublicDocumentPage; 0
// keeps the default and invalid values are left for listDocuments to
// refuse.
func publicPageLimit(limit int) int {
	if limit > MaxPublicDocumentPage && limit <= MaxDocumentPage {
		return MaxPublicDocumentPage
	}
	return limit
}

// AllowDataRead counts one visitor read of site data from ip and refuses
// it with a rate limit error past DataReadsPerIP a minute (IPv6 by /64).
func (s *Service) AllowDataRead(ip string) error {
	if ok, retry := s.dataReadIP.Allow(ipKey(ip)); !ok {
		return domain.ErrRateLimited.WithRetryAfter(retry)
	}
	return nil
}
