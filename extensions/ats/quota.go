package main

import (
	"maps"
	"math"
	"strings"
	"time"
)

// QuotaStatus represents the normalized model quota state from a generic Quota Provider or Quota Gateway.
type QuotaStatus struct {
	Provider         string             `json:"provider"`
	RemainingPct     float64            `json:"remaining_pct"`
	UsedRatio        float64            `json:"used_ratio"`
	Exhausted        bool               `json:"exhausted"`
	CooldownUntil    *time.Time         `json:"cooldown_until,omitempty"`
	ResetAt          *time.Time         `json:"reset_at,omitempty"`
	ActiveAccounts   int                `json:"active_accounts"`
	TotalAccounts    int                `json:"total_accounts"`
	TierRemainingPct map[string]float64 `json:"tier_remaining_pct"`
	CheckedAt        time.Time          `json:"checked_at"`
	Note             string             `json:"note,omitempty"`
}

// DefaultQuotaStatus returns a healthy 100% quota snapshot when no external quota limit is active.
func DefaultQuotaStatus() QuotaStatus {
	return QuotaStatus{
		Provider:       "default",
		RemainingPct:   100,
		UsedRatio:      0,
		Exhausted:      false,
		ActiveAccounts: 1,
		TotalAccounts:  1,
		TierRemainingPct: map[string]float64{
			TierFast:     100,
			TierStandard: 100,
			TierStrong:   100,
		},
		CheckedAt: time.Now().UTC(),
	}
}

// SetQuotaStatus updates the queue's cached generic Quota Provider status and persists it.
func (q *Queue) SetQuotaStatus(st QuotaStatus) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if st.TierRemainingPct == nil {
		st.TierRemainingPct = map[string]float64{
			TierFast:     st.RemainingPct,
			TierStandard: st.RemainingPct,
			TierStrong:   st.RemainingPct,
		}
	} else {
		st.TierRemainingPct = maps.Clone(st.TierRemainingPct)
	}
	q.quota = st
	return q.saveLocked()
}

// QuotaStatus returns the queue's current generic Quota Provider status.
func (q *Queue) QuotaStatus() QuotaStatus {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := q.quota
	if out.TierRemainingPct != nil {
		out.TierRemainingPct = maps.Clone(out.TierRemainingPct)
	}
	return out
}

// GenericGatewayAccounts matches the Hub's `usage.gateway_accounts` wire contract.
type GenericGatewayAccounts struct {
	Sources  []GenericGatewaySource  `json:"sources"`
	Accounts []GenericGatewayAccount `json:"accounts"`
}

// GenericGatewaySource describes one usage source in `usage.gateway_accounts`.
type GenericGatewaySource struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// GenericGatewayAccount describes one account entry in `usage.gateway_accounts`.
type GenericGatewayAccount struct {
	Source        string                 `json:"source"`
	AccountID     string                 `json:"account_id"`
	Name          string                 `json:"name"`
	Provider      string                 `json:"provider"`
	Plan          string                 `json:"plan,omitempty"`
	Status        string                 `json:"status"`
	Schedulable   *bool                  `json:"schedulable,omitempty"`
	Reason        string                 `json:"reason,omitempty"`
	CooldownUntil *time.Time             `json:"cooldown_until,omitempty"`
	Windows       []GenericGatewayWindow `json:"windows,omitempty"`
	CheckedAt     *time.Time             `json:"checked_at,omitempty"`
	Stale         bool                   `json:"stale,omitempty"`
}

// GenericGatewayWindow describes one quota window of an account or model.
type GenericGatewayWindow struct {
	Kind        string     `json:"kind,omitempty"`
	Label       string     `json:"label,omitempty"`
	Scope       string     `json:"scope,omitempty"`
	ScopedModel string     `json:"scoped_model,omitempty"`
	UsedRatio   *float64   `json:"used_ratio,omitempty"`
	Used        *float64   `json:"used,omitempty"`
	Limit       *float64   `json:"limit,omitempty"`
	Remaining   *float64   `json:"remaining,omitempty"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
	Exhausted   bool       `json:"exhausted,omitempty"`
}

// ParseGatewayAccounts converts the Hub's `usage.gateway_accounts` response into a normalized QuotaStatus.
func ParseGatewayAccounts(ga GenericGatewayAccounts, now time.Time) QuotaStatus {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	if len(ga.Accounts) == 0 {
		st := DefaultQuotaStatus()
		st.CheckedAt = now
		if len(ga.Sources) > 0 && ga.Sources[0].Name != "" {
			st.Provider = ga.Sources[0].Name
		}
		return st
	}

	providerName := "usage.gateway_accounts"
	if len(ga.Sources) > 0 && strings.TrimSpace(ga.Sources[0].Name) != "" {
		providerName = strings.TrimSpace(ga.Sources[0].Name)
	}

	totalAccounts := len(ga.Accounts)
	activeAccounts := 0
	var sumRemaining float64
	var earliestReset *time.Time
	var earliestCooldown *time.Time

	tierSums := map[string]float64{}
	tierCounts := map[string]int{}

	for _, acc := range ga.Accounts {
		status := strings.ToLower(strings.TrimSpace(acc.Status))
		schedulable := true
		if acc.Schedulable != nil {
			schedulable = *acc.Schedulable
		}
		inCooldown := acc.CooldownUntil != nil && acc.CooldownUntil.After(now)
		if inCooldown {
			t := acc.CooldownUntil.UTC()
			if earliestCooldown == nil || t.Before(*earliestCooldown) {
				earliestCooldown = &t
			}
		}

		isActive := schedulable && !inCooldown && (status == "" || status == "active" || status == "ok" || status == "healthy")
		accRem := 100.0
		if len(acc.Windows) > 0 {
			minRem, winReset, winTiers, winExhausted := summarizeWindows(acc.Windows)
			accRem = minRem
			if winExhausted {
				isActive = false
				accRem = 0
			}
			if winReset != nil && (earliestReset == nil || winReset.Before(*earliestReset)) {
				earliestReset = winReset
			}
			if isActive && accRem > 0 {
				for tk, tv := range winTiers {
					tierSums[tk] += tv
					tierCounts[tk]++
				}
			}
		} else if !isActive {
			accRem = 0
		}

		if isActive && accRem > 0 {
			activeAccounts++
			sumRemaining += accRem
		}
	}

	var overallRem float64
	exhausted := false
	if activeAccounts == 0 {
		overallRem = 0
		exhausted = true
	} else {
		// Average remaining quota across active accounts scaled by healthy account availability.
		overallRem = roundPct(sumRemaining / float64(totalAccounts))
		if overallRem <= 0 {
			exhausted = true
		}
	}

	tierRem := map[string]float64{
		TierFast:     overallRem,
		TierStandard: overallRem,
		TierStrong:   overallRem,
	}
	if activeAccounts > 0 {
		availScale := float64(activeAccounts) / float64(totalAccounts)
		for _, tk := range []string{TierFast, TierStandard, TierStrong} {
			if c := tierCounts[tk]; c > 0 {
				tierRem[tk] = roundPct((tierSums[tk] / float64(c)) * availScale)
			}
		}
	}

	// Account-level cooldown only affects activeAccounts and RemainingPct when other schedulable accounts
	// are still active; only set global CooldownUntil when all schedulable accounts are inactive/cooling.
	var globalCooldown *time.Time
	if activeAccounts == 0 {
		globalCooldown = earliestCooldown
	}

	resetAt := earliestReset
	if globalCooldown != nil && (resetAt == nil || globalCooldown.Before(*resetAt)) {
		resetAt = globalCooldown
	}

	return QuotaStatus{
		Provider:         providerName,
		RemainingPct:     overallRem,
		UsedRatio:        clampRatio(1.0 - overallRem/100.0),
		Exhausted:        exhausted,
		CooldownUntil:    globalCooldown,
		ResetAt:          resetAt,
		ActiveAccounts:   activeAccounts,
		TotalAccounts:    totalAccounts,
		TierRemainingPct: tierRem,
		CheckedAt:        now,
	}
}

func summarizeWindows(ws []GenericGatewayWindow) (minRemaining float64, earliestReset *time.Time, tierRemaining map[string]float64, anyExhausted bool) {
	minRemaining = 100.0
	tierRemaining = make(map[string]float64)
	for _, w := range ws {
		rem := windowRemainingPct(w)
		if w.Exhausted || rem <= 0 {
			rem = 0
			anyExhausted = true
		}
		if w.ScopedModel != "" {
			tier := classifyModelTier(w.ScopedModel)
			if cur, ok := tierRemaining[tier]; !ok || rem < cur {
				tierRemaining[tier] = rem
			}
		} else {
			if rem < minRemaining {
				minRemaining = rem
			}
		}
		if w.ResetsAt != nil {
			t := w.ResetsAt.UTC()
			if earliestReset == nil || t.Before(*earliestReset) {
				earliestReset = &t
			}
		}
	}
	if len(tierRemaining) > 0 && minRemaining == 100.0 {
		for _, v := range tierRemaining {
			if v < minRemaining {
				minRemaining = v
			}
		}
	}
	for _, tk := range []string{TierFast, TierStandard, TierStrong} {
		if _, ok := tierRemaining[tk]; !ok {
			tierRemaining[tk] = minRemaining
		}
	}
	return roundPct(minRemaining), earliestReset, tierRemaining, anyExhausted
}

func windowRemainingPct(w GenericGatewayWindow) float64 {
	if w.Exhausted {
		return 0
	}
	if w.UsedRatio != nil {
		return roundPct((1.0 - clampRatio(*w.UsedRatio)) * 100.0)
	}
	if w.Limit != nil && *w.Limit > 0 {
		if w.Remaining != nil {
			return roundPct(clampRatio(*w.Remaining / *w.Limit) * 100.0)
		}
		if w.Used != nil {
			return roundPct((1.0 - clampRatio(*w.Used / *w.Limit)) * 100.0)
		}
	}
	return 100.0
}

func classifyModelTier(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(m, "haiku") || strings.Contains(m, "fast") || strings.Contains(m, "mini") || strings.Contains(m, "flash"):
		return TierFast
	case strings.Contains(m, "opus") || strings.Contains(m, "fable") || strings.Contains(m, "strong") || strings.Contains(m, "pro"):
		return TierStrong
	default:
		return TierStandard
	}
}

func clampRatio(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampPct(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return roundPct(v)
}

func roundPct(v float64) float64 {
	return math.Round(v*10) / 10
}
