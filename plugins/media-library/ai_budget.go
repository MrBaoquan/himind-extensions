package main

import (
	"database/sql"
	"fmt"
	"math"
	"time"
)

// AI 预算闸门：按 token 用量估算花费，限制单次作业、单日与单月。
// 单价按 deepseek-v4.1-flash 公开价近似（USD / 百万 token）：
// 输入 0.15、输出 0.60。缓存只记总 token，按 8:2 的输入输出比例折算成混合单价。
const (
	aiInputPricePerMillion  = 0.15
	aiOutputPricePerMillion = 0.60
	aiInputShare            = 0.8
	aiJobCapUSD             = 2.0
	aiDayCapUSD             = 5.0
	aiMonthCapUSD           = 20.0
)

func aiBlendedPricePerMillion() float64 {
	return aiInputPricePerMillion*aiInputShare + aiOutputPricePerMillion*(1-aiInputShare)
}

func aiTokensToUSD(tokens int64) float64 {
	return float64(tokens) * aiBlendedPricePerMillion() / 1_000_000
}

func aiTokensForUSD(budget float64) int64 {
	if budget <= 0 {
		return 0
	}
	return int64(math.Floor(budget / aiBlendedPricePerMillion() * 1_000_000))
}

type aiBudgetInput struct {
	// 已缓存且完成打标的组数，用于估算本次作业花费。
	PlannedCalls int `json:"planned_calls"`
}

type aiBudget struct {
	tokensToday    int64
	tokensMonth    int64
	callsToday     int64
	callsMonth     int64
	plannedTokens  int64
	plannedUSD     float64
	nextBudget     int64
	allow          bool
	blockedReason  string
	perCallTokens  int64
	remainingDay   float64
	remainingMonth float64
}

func loadAIBudget(in aiBudgetInput) (*aiBudget, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	budget := &aiBudget{perCallTokens: 700}
	if err := db.QueryRow(`SELECT COALESCE(SUM(tokens),0),COUNT(*) FROM ai_tag_cache WHERE created_at>=?`, startOfDay).
		Scan(&budget.tokensToday, &budget.callsToday); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err := db.QueryRow(`SELECT COALESCE(SUM(tokens),0),COUNT(*) FROM ai_tag_cache WHERE created_at>=?`, startOfMonth).
		Scan(&budget.tokensMonth, &budget.callsMonth); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if in.PlannedCalls < 0 {
		in.PlannedCalls = 0
	}
	budget.plannedTokens = int64(in.PlannedCalls) * budget.perCallTokens
	budget.plannedUSD = aiTokensToUSD(budget.plannedTokens)
	budget.remainingDay = aiDayCapUSD - aiTokensToUSD(budget.tokensToday)
	budget.remainingMonth = aiMonthCapUSD - aiTokensToUSD(budget.tokensMonth)
	jobTokens := aiTokensForUSD(aiJobCapUSD)
	dayTokens := aiTokensForUSD(budget.remainingDay)
	monthTokens := aiTokensForUSD(budget.remainingMonth)
	budget.nextBudget = jobTokens
	if dayTokens < budget.nextBudget {
		budget.nextBudget = dayTokens
	}
	if monthTokens < budget.nextBudget {
		budget.nextBudget = monthTokens
	}
	budget.allow = budget.nextBudget >= int64(in.PlannedCalls)*budget.perCallTokens/2
	switch {
	case budget.remainingMonth <= 0:
		budget.blockedReason = fmt.Sprintf("本月 AI 预算已用完（上限 $%.0f）", aiMonthCapUSD)
		budget.nextBudget = 0
	case budget.remainingDay <= 0:
		budget.blockedReason = fmt.Sprintf("今日 AI 预算已用完（上限 $%.0f），明天再来", aiDayCapUSD)
		budget.nextBudget = 0
	case budget.plannedUSD > aiJobCapUSD:
		budget.blockedReason = fmt.Sprintf("本次预估 $%.2f 超过单次上限 $%.0f，先缩小范围", budget.plannedUSD, aiJobCapUSD)
		budget.allow = false
	}
	return budget, nil
}

func aiBudgetSummary(in aiBudgetInput) (any, error) {
	budget, err := loadAIBudget(in)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"price_per_million_usd": aiBlendedPricePerMillion(),
		"caps_usd": map[string]any{
			"job": aiJobCapUSD, "day": aiDayCapUSD, "month": aiMonthCapUSD,
		},
		"used_tokens": map[string]any{"today": budget.tokensToday, "month": budget.tokensMonth},
		"calls":       map[string]any{"today": budget.callsToday, "month": budget.callsMonth},
		"estimated_usd": map[string]any{
			"today": aiTokensToUSD(budget.tokensToday), "month": aiTokensToUSD(budget.tokensMonth),
		},
		"remaining_usd": map[string]any{
			"day": math.Max(0, budget.remainingDay), "month": math.Max(0, budget.remainingMonth),
		},
		"planned": map[string]any{
			"calls": in.PlannedCalls, "tokens": budget.plannedTokens, "usd": budget.plannedUSD,
		},
		"next_job_budget_tokens": budget.nextBudget,
		"allowed":                budget.allow,
		"blocked_reason":         budget.blockedReason,
	}, nil
}
