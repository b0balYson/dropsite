package service

import (
	"context"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// Daily budget of proxy endpoints. Days are UTC, like visit statistics.

// proxyBudgetKeepDays is how long daily counters are kept.
const proxyBudgetKeepDays = 7

func budgetDay(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// untilNextDay is the time left until the next UTC midnight.
func untilNextDay(now time.Time) time.Duration {
	n := now.UTC()
	next := time.Date(n.Year(), n.Month(), n.Day()+1, 0, 0, 0, 0, time.UTC)
	return next.Sub(n)
}

func errProxyBudget(now time.Time) error {
	return domain.E(domain.KindRateLimited, domain.CodeProxyBudget,
		"the daily budget of this endpoint is used up; it works again after midnight UTC").
		WithRetryAfter(untilNextDay(now))
}

func (s *Service) budgetExhausted(id int64, day string) bool {
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	return s.exhausted[id] == day
}

func (s *Service) markExhausted(id int64, day string) {
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	for k, d := range s.exhausted {
		if d != day {
			delete(s.exhausted, k)
		}
	}
	s.exhausted[id] = day
}

// spendBudget counts one call against the endpoint's daily budget, before
// it is forwarded. Once the budget is used up, further calls of the day are
// refused from memory without a database write.
func (s *Service) spendBudget(ctx context.Context, site domain.Site, e domain.ProxyEndpoint) error {
	budget := ProxyEffectiveLimits(e.ProxyConfig).DailyBudget
	if budget <= 0 {
		return nil
	}
	now := s.now()
	day := budgetDay(now)
	if s.budgetExhausted(e.ID, day) {
		return errProxyBudget(now)
	}
	n, err := s.repo.AddProxyDailyCall(ctx, e.ID, day)
	if err != nil {
		return err
	}
	if n <= budget {
		return nil
	}
	s.markExhausted(e.ID, day)
	s.notifyBudget(context.WithoutCancel(ctx), site, e, day, budget)
	return errProxyBudget(now)
}

// notifyBudget tells the owner, once per endpoint and day, that the budget
// ran out: an audit entry (the web interface shows the pause from the
// counters) and a message through the site's active Telegram and e-mail
// form channels, if any.
func (s *Service) notifyBudget(ctx context.Context, site domain.Site, e domain.ProxyEndpoint, day string, budget int) {
	first, err := s.repo.MarkProxyBudgetNotified(ctx, e.ID, day)
	if err != nil || !first {
		if err != nil {
			s.log.Warn("proxy budget notification", "endpoint", e.ID, "err", err)
		}
		return
	}
	s.audit(ctx, Actor{}, "proxy.budget.exhausted", siteTarget(site.ID), map[string]any{
		"name": e.Name, "budget": budget, "day": day, "source": "guard",
	})
	chs, err := s.repo.ListFormChannels(ctx, site.ID)
	if err != nil || len(chs) == 0 {
		return
	}
	cfg, err := s.formConfig(ctx)
	if err != nil {
		return
	}
	lang := "en"
	if u, err := s.repo.GetUser(ctx, site.OwnerID); err == nil {
		lang = ownerLang(u.Lang)
	}
	subject, text := budgetMessage(lang, s.siteHost(ctx, site), e.Name, budget, s.ProxyPageURL(site.ID, e.Name))
	go func() {
		ctx, cancel := context.WithTimeout(ctx, formDeliveryTimeout)
		defer cancel()
		for _, ch := range chs {
			if !ch.Enabled || ch.Status != domain.ChannelActive || ch.PausedAt != nil {
				continue
			}
			var err error
			switch ch.Kind {
			case domain.ChannelTelegram:
				if cfg.telegramToken == "" {
					continue
				}
				err = s.senders.TelegramSend(ctx, cfg.telegramToken, ch.Target, text)
			case domain.ChannelEmail:
				if !cfg.smtp.Configured() {
					continue
				}
				err = s.senders.Email(ctx, cfg.smtp, ch.Target, subject, text)
			default:
				continue
			}
			if err != nil {
				s.log.Warn("proxy budget notification not sent", "site", site.ID, "channel", ch.ID, "err", forms.ErrDetail(err))
			}
		}
	}()
}

func budgetMessage(lang, host, name string, budget int, link string) (string, string) {
	switch lang {
	case "ru":
		return "Дневной лимит ключа API исчерпан",
			fmt.Sprintf("Сайт %s: эндпоинт «%s» израсходовал дневной лимит (%d вызовов) и приостановлен до полуночи UTC.\n"+
				"Если это ожидаемая нагрузка, увеличьте лимит: %s", host, name, budget, link)
	case "zh":
		return "API 密钥的每日额度已用完",
			fmt.Sprintf("网站 %s：端点「%s」已用完每日额度（%d 次调用），将暂停到 UTC 午夜。\n如果这是正常流量，请提高额度：%s",
				host, name, budget, link)
	}
	return "Daily API key budget used up",
		fmt.Sprintf("Site %s: endpoint %q used up its daily budget (%d calls) and is paused until midnight UTC.\n"+
			"If this is expected traffic, raise the budget: %s", host, name, budget, link)
}

// withTodayUsage fills the calls of the current day into the endpoints.
func (s *Service) withTodayUsage(ctx context.Context, siteID string, eps []domain.ProxyEndpoint) []domain.ProxyEndpoint {
	day := budgetDay(s.now())
	calls, err := s.repo.ProxyDailyCalls(ctx, siteID, day)
	if err != nil {
		s.log.Warn("proxy daily usage", "site", siteID, "err", err)
		return eps
	}
	for i := range eps {
		n, b := calls[eps[i].ID], ProxyEffectiveLimits(eps[i].ProxyConfig).DailyBudget
		// Refused calls are counted too; the owner sees the budget, not more.
		eps[i].Usage.Paused = b > 0 && n > b
		if eps[i].Usage.Paused {
			n = b
		}
		eps[i].Usage.Today = n
	}
	return eps
}

// purgeProxyDaily drops old daily counters (Housekeeping).
func (s *Service) purgeProxyDaily(ctx context.Context) error {
	return s.repo.DeleteProxyDailyBefore(ctx, budgetDay(s.now().AddDate(0, 0, -proxyBudgetKeepDays)))
}
