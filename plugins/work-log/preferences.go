package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 本文件是「日志库级偏好」的持久化与财年口径计算。
//
// 目前只有一项：季度 / 年度的起算月（fiscal_year_start_month）。
//
// 为什么存在日志根而不是本机 state.json：起算月是公司口径，跟着日志库走才能在换机器、
// 共享库、备份恢复后保持一致；state.json 记的是本机路径，属于设备事实。缺省值 1 表示
// 自然季度（1/2/3 月为 Q1），与历史归档的报告键完全兼容 —— 起算月为 1 时季度键仍是
// `2026-Q4`，年度键仍是 `2026`。
//
// 起算月为 2 时的语义：财年从 2 月开始，Q1 = 2/3/4 月，Q2 = 5/6/7 月，Q3 = 8/9/10 月，
// Q4 = 11/12/次年 1 月；键里的年份是「财年起始年」，所以 2026-01-15 属于 2025-Q4。
// 年度总结同样按起算月划分（2026 年度 = 2026-02-01 ~ 2027-01-31），否则季度与年度
// 的口径会互相打架。

const preferencesSchema = "work-log-preferences.v1"

// defaultFiscalYearStartMonth 是缺省起算月：1 月，即自然季度。
const defaultFiscalYearStartMonth = 1

type preferencesFile struct {
	SchemaVersion        string `json:"schema_version"`
	UpdatedAt            string `json:"updated_at"`
	FiscalYearStartMonth int    `json:"fiscal_year_start_month"`
}

func preferencesPath(root string) string {
	return filepath.Join(root, "config", "preferences.json")
}

// normalizeFiscalYearStartMonth 把越界值收敛到缺省 1 月。
//
// 读取侧故意不报错：偏好文件被手改成 13 时，宁可退回自然季度继续出报告，
// 也不要让「季度总结」整块功能因为一个数字打不开。写入侧仍严格拒绝越界值。
func normalizeFiscalYearStartMonth(value int) int {
	if value < 1 || value > 12 {
		return defaultFiscalYearStartMonth
	}
	return value
}

func readPreferences(root string) (preferencesFile, error) {
	fallback := preferencesFile{
		SchemaVersion:        preferencesSchema,
		FiscalYearStartMonth: defaultFiscalYearStartMonth,
	}
	body, err := os.ReadFile(preferencesPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	if err != nil {
		return fallback, errors.New("偏好配置读取失败")
	}
	var prefs preferencesFile
	if err := json.Unmarshal(body, &prefs); err != nil {
		return fallback, errors.New("偏好配置不是合法 JSON")
	}
	prefs.SchemaVersion = preferencesSchema
	prefs.FiscalYearStartMonth = normalizeFiscalYearStartMonth(prefs.FiscalYearStartMonth)
	return prefs, nil
}

func writePreferences(root string, prefs preferencesFile) error {
	prefs.SchemaVersion = preferencesSchema
	prefs.UpdatedAt = time.Now().Format(time.RFC3339)
	prefs.FiscalYearStartMonth = normalizeFiscalYearStartMonth(prefs.FiscalYearStartMonth)
	payload, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return errors.New("偏好配置序列化失败")
	}
	if err := writeFileAtomic(preferencesPath(root), append(payload, '\n')); err != nil {
		return errors.New("偏好配置写入失败")
	}
	return nil
}

// fiscalStartMonth 读取当前日志库的起算月；任何读取问题都退回自然季度。
//
// 报告渲染与汇聚都走这里，保证「读」和「写」用的口径是同一份配置。
func fiscalStartMonth(root string) int {
	prefs, err := readPreferences(root)
	if err != nil {
		return defaultFiscalYearStartMonth
	}
	return normalizeFiscalYearStartMonth(prefs.FiscalYearStartMonth)
}

// ---------- 财年口径 ----------

// fiscalYearSpan 是按起算月展开的一个财年。
type fiscalYearSpan struct {
	Year  int
	Start time.Time
	End   time.Time
}

// Key 是年度归档键：财年起始年（起算月为 1 时与日历年一致）。
func (span fiscalYearSpan) Key() string {
	return fmt.Sprintf("%04d", span.Year)
}

// fiscalQuarterSpan 是按起算月展开的一个季度。
type fiscalQuarterSpan struct {
	// FiscalYear 是该季度所属财年的起始年，用于组装稳定的归档键。
	FiscalYear int
	// Index 是财年内序号 1..4。
	Index int
	Start time.Time
	End   time.Time
}

// Key 是季度归档键，例如 `2026-Q1`。
func (span fiscalQuarterSpan) Key() string {
	return fmt.Sprintf("%04d-Q%d", span.FiscalYear, span.Index)
}

// fiscalYearOf 把日期落到所属财年。
func fiscalYearOf(date time.Time, startMonth int) fiscalYearSpan {
	startMonth = normalizeFiscalYearStartMonth(startMonth)
	// 用「月序」做绝对比较，避免跨年时的分支判断：abs 是公元 0 年起的月数。
	abs := date.Year()*12 + int(date.Month()) - 1
	startAbs := date.Year()*12 + startMonth - 1
	if startAbs > abs {
		// 日期还早于今年的起算月，说明它落在上一个财年里。
		startAbs -= 12
	}
	year := startAbs / 12
	start := time.Date(year, time.Month(startMonth), 1, 0, 0, 0, 0, time.UTC)
	return fiscalYearSpan{Year: year, Start: start, End: start.AddDate(1, 0, -1)}
}

// fiscalQuarterOf 把日期落到所属季度。季度边界始终是起算月 + 3k 个月，
// 因此任意日期与季度起点的月差除以 3 就是序号，不存在跨年特判。
func fiscalQuarterOf(date time.Time, startMonth int) fiscalQuarterSpan {
	year := fiscalYearOf(date, startMonth)
	offset := (date.Year()-year.Start.Year())*12 + int(date.Month()) - int(year.Start.Month())
	index := offset / 3
	start := year.Start.AddDate(0, index*3, 0)
	return fiscalQuarterSpan{
		FiscalYear: year.Year,
		Index:      index + 1,
		Start:      start,
		End:        start.AddDate(0, 3, -1),
	}
}

// fiscalQuarterRuleText 用一句话说明当前起算月下的季度划分，供界面与 Agent 直接引用。
func fiscalQuarterRuleText(startMonth int) string {
	startMonth = normalizeFiscalYearStartMonth(startMonth)
	parts := make([]string, 0, 4)
	for index := 0; index < 4; index++ {
		from := monthWithinYear(startMonth + index*3)
		to := monthWithinYear(startMonth + index*3 + 2)
		if to < from {
			// 跨年的季度写成「11 月-次年 1 月」，比「11-1 月」更好读。
			parts = append(parts, fmt.Sprintf("Q%d %d 月-次年 %d 月", index+1, from, to))
			continue
		}
		parts = append(parts, fmt.Sprintf("Q%d %d-%d 月", index+1, from, to))
	}
	return strings.Join(parts, " · ")
}

// monthWithinYear 把可能超过 12 的月序折回 1..12（用于生成规则文案）。
func monthWithinYear(value int) int {
	return (value-1)%12 + 1
}

// ---------- 能力 ----------

func preferencesGetCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	prefs, err := readPreferences(root)
	if err != nil {
		return nil, err
	}
	path := preferencesPath(root)
	_, statErr := os.Stat(path)
	return map[string]any{
		"log_root":                root,
		"config_path":             path,
		"exists":                  statErr == nil,
		"fiscal_year_start_month": prefs.FiscalYearStartMonth,
		"quarter_rule":            fiscalQuarterRuleText(prefs.FiscalYearStartMonth),
	}, nil
}

// preferencesSaveCap 保存起算月。1-12 之外一律拒绝：静默收敛会让用户在界面上
// 选了个值却得到另一个口径，比直接报错更难排查。
func preferencesSaveCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	if in.FiscalYearStartMonth == nil {
		return nil, errors.New("fiscal_year_start_month 必填（1-12）")
	}
	month := *in.FiscalYearStartMonth
	if month < 1 || month > 12 {
		return nil, errors.New("fiscal_year_start_month 必须是 1-12 的整数")
	}
	unlock := lockRoot(root)
	defer unlock()
	prefs, err := readPreferences(root)
	if err != nil {
		// 偏好文件损坏时以缺省值为基底重写，避免用户被坏文件卡死。
		prefs = preferencesFile{FiscalYearStartMonth: defaultFiscalYearStartMonth}
	}
	prefs.FiscalYearStartMonth = month
	if err := writePreferences(root, prefs); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root":                root,
		"config_path":             preferencesPath(root),
		"exists":                  true,
		"saved":                   true,
		"fiscal_year_start_month": month,
		"quarter_rule":            fiscalQuarterRuleText(month),
	}, nil
}
