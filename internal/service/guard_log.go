package service

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/b0balYson/dropsite/internal/guard"
	"github.com/b0balYson/dropsite/internal/stats"
)

// guardLog is the ban detector's own position in the sites access log. It
// starts at the end of the log: only traffic from now on matters.
type guardLog struct {
	cur     stats.Cursor
	started bool
}

// RunGuard reads the sites access log every interval for violations of the
// global rate limit and keeps the ban markers current, until ctx is done.
func (s *Service) RunGuard(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var lg guardLog
	lastExpire := time.Time{}
	for {
		if s.now().Sub(lastExpire) >= banRefresh {
			if err := s.expireBans(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error("expire bans", "err", err)
			}
			lastExpire = s.now()
		}
		if err := s.scanAccessLog(ctx, &lg); err != nil && !errors.Is(err, context.Canceled) {
			s.log.Error("ban detector", "err", err)
			if errors.Is(err, stats.ErrNoInode) {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// scanAccessLog processes the lines written since the last call.
func (s *Service) scanAccessLog(ctx context.Context, lg *guardLog) error {
	path := s.cfg.StatsLogFile
	if path == "" {
		return nil
	}
	if !lg.started {
		cur, err := stats.EndCursor(path)
		if err != nil {
			return err
		}
		lg.cur, lg.started = cur, true
		return nil
	}
	bans, _ := s.loadBans(ctx, false)
	watchV6 := false
	for k := range bans {
		if guard.IsNet(k) {
			watchV6 = true
			break
		}
	}
	for range guardMaxReads {
		ch, err := stats.ReadLog(path, lg.cur, guardReadBytes)
		if err != nil {
			return err
		}
		if ch.Done(lg.cur) {
			return nil
		}
		for _, line := range ch.Lines {
			// Most lines carry neither marker: skip the JSON decoding.
			if !watchV6 && !bytes.Contains(line, []byte(guard.BlockHeader)) {
				continue
			}
			s.processAccess(ctx, line)
		}
		lg.cur = ch.Next
	}
	return nil
}

func (s *Service) processAccess(ctx context.Context, line []byte) {
	a, ok := guard.ParseAccess(line)
	if !ok || s.now().Sub(a.Time) > guardMaxLineAge {
		return
	}
	switch a.Block {
	case guard.BlockRate:
		if s.violations.Add(guard.NetKey(a.IP), a.Time) {
			if _, err := s.banNet(ctx, a.IP, BanReasonRate, 0); err != nil {
				s.log.Error("ban network", "ip", a.IP, "err", err)
			}
		}
	default:
		s.coverBannedAddress(ctx, a.IP)
	}
}
