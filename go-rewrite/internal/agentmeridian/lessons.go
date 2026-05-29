package agentmeridian

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"meridian-go-rewrite/internal/config"
	"meridian-go-rewrite/internal/hivemind"
	"meridian-go-rewrite/internal/logger"
)

const (
	minEvolvePositions = 5
	maxChangePerStep   = 0.20
	maxManualLessonLen = 400
)

type PerformanceRecord struct {
	Position        string                 `json:"position"`
	Pool            string                 `json:"pool"`
	PoolName        string                 `json:"pool_name"`
	BaseMint        string                 `json:"base_mint,omitempty"`
	Strategy        string                 `json:"strategy"`
	BinRange        interface{}            `json:"bin_range"`
	BinStep         int                    `json:"bin_step"`
	Volatility      float64                `json:"volatility"`
	FeeTvlRatio     float64                `json:"fee_tvl_ratio"`
	OrganicScore    float64                `json:"organic_score"`
	AmountSol       float64                `json:"amount_sol"`
	FeesEarnedUsd   float64                `json:"fees_earned_usd"`
	FeesEarnedSol   float64                `json:"fees_earned_sol,omitempty"`
	FinalValueUsd   float64                `json:"final_value_usd"`
	InitialValueUsd float64                `json:"initial_value_usd"`
	MinutesInRange  float64                `json:"minutes_in_range"`
	MinutesHeld     float64                `json:"minutes_held"`
	CloseReason     string                 `json:"close_reason"`
	PnlUsd          float64                `json:"pnl_usd"`
	PnlPct          float64                `json:"pnl_pct"`
	RangeEfficiency float64                `json:"range_efficiency"`
	RecordedAt      string                 `json:"recorded_at"`
	DeployedAt      string                 `json:"deployed_at,omitempty"`
	SignalSnapshot  map[string]interface{} `json:"signal_snapshot,omitempty"`
}

type Lesson struct {
	ID              int64    `json:"id"`
	Rule            string   `json:"rule"`
	Tags            []string `json:"tags"`
	Outcome         string   `json:"outcome"`
	SourceType      string   `json:"sourceType"`
	Confidence      float64  `json:"confidence,omitempty"`
	Context         string   `json:"context,omitempty"`
	PnlPct          float64  `json:"pnl_pct,omitempty"`
	FeesEarnedUsd   float64  `json:"fees_earned_usd,omitempty"`
	InitialValueUsd float64  `json:"initial_value_usd,omitempty"`
	RangeEfficiency float64  `json:"range_efficiency,omitempty"`
	CloseReason     string   `json:"close_reason,omitempty"`
	Pool            string   `json:"pool,omitempty"`
	CreatedAt       string   `json:"created_at"`
	Pinned          bool     `json:"pinned,omitempty"`
	Role            string   `json:"role,omitempty"`
}

type LessonsData struct {
	Lessons     []Lesson            `json:"lessons"`
	Performance []PerformanceRecord `json:"performance"`
}

var (
	lessonsMutex sync.RWMutex
	lessonsFile  string
)

func initLessonsPath() {
	if lessonsFile == "" {
		cfg := config.Get()
		if cfg != nil {
			lessonsFile = cfg.DataPath("lessons.json")
		} else {
			lessonsFile = "lessons.json"
		}
	}
}

func loadLessonsData() LessonsData {
	initLessonsPath()
	lessonsMutex.RLock()
	defer lessonsMutex.RUnlock()

	data, err := os.ReadFile(lessonsFile)
	if err != nil {
		return LessonsData{Lessons: []Lesson{}, Performance: []PerformanceRecord{}}
	}

	var ld LessonsData
	if err := json.Unmarshal(data, &ld); err != nil {
		logger.Error("lessons_warn", fmt.Errorf("Invalid %s: %w", lessonsFile, err))
		return LessonsData{Lessons: []Lesson{}, Performance: []PerformanceRecord{}}
	}
	return ld
}

func saveLessonsData(ld LessonsData) {
	initLessonsPath()
	lessonsMutex.Lock()
	defer lessonsMutex.Unlock()

	if dir := filepath.Dir(lessonsFile); dir != "." {
		os.MkdirAll(dir, 0755)
	}

	bytes, err := json.MarshalIndent(ld, "", "  ")
	if err != nil {
		logger.Error("lessons_error", fmt.Errorf("Failed to marshal lessons: %w", err))
		return
	}
	os.WriteFile(lessonsFile, bytes, 0644)
}

func RecordPerformance(perf PerformanceRecord) {
	ld := loadLessonsData()

	suspiciousUnitMix := perf.InitialValueUsd >= 20 && perf.AmountSol >= 0.25 &&
		perf.FinalValueUsd > 0 && perf.FinalValueUsd <= perf.AmountSol*2
	if suspiciousUnitMix {
		logger.Log("lessons_warn", fmt.Sprintf("Skipped suspicious record for %s: initial=%v, final=%v, amount_sol=%v", perf.PoolName, perf.InitialValueUsd, perf.FinalValueUsd, perf.AmountSol))
		return
	}

	pnlUsd := (perf.FinalValueUsd + perf.FeesEarnedUsd) - perf.InitialValueUsd
	pnlPct := float64(0)
	if perf.InitialValueUsd > 0 {
		pnlPct = (pnlUsd / perf.InitialValueUsd) * 100
	}
	rangeEfficiency := float64(0)
	if perf.MinutesHeld > 0 {
		rangeEfficiency = (perf.MinutesInRange / perf.MinutesHeld) * 100
	}

	closeReasonText := strings.ToLower(perf.CloseReason)
	suspiciousAbsurdClosedPnl := perf.InitialValueUsd >= 20 && pnlPct <= -90 && !strings.Contains(closeReasonText, "stop loss")

	if suspiciousAbsurdClosedPnl {
		logger.Log("lessons_warn", fmt.Sprintf("Skipped absurd closed PnL record for %s: pnl_pct=%.2f reason=%s", perf.PoolName, pnlPct, perf.CloseReason))
		return
	}

	perf.PnlUsd = math.Round(pnlUsd*10000) / 10000
	perf.PnlPct = math.Round(pnlPct*100) / 100
	perf.RangeEfficiency = math.Round(rangeEfficiency*10) / 10
	perf.RecordedAt = time.Now().UTC().Format(time.RFC3339)

	ld.Performance = append(ld.Performance, perf)

	lesson := deriveLesson(perf)
	if lesson != nil {
		ld.Lessons = append(ld.Lessons, *lesson)
		logger.Log("lessons", "New lesson: "+lesson.Rule)
	}

	saveLessonsData(ld)

	// In real node app, we trigger evolveThresholds here, we'll skip for now or do simplified version.
	if len(ld.Performance)%minEvolvePositions == 0 {
		cfg := config.Get()
		evolveThresholds(ld.Performance, cfg)
	}
}

func deriveLesson(perf PerformanceRecord) *Lesson {
	tags := []string{}
	feeYieldPct := float64(0)
	if perf.InitialValueUsd > 0 {
		feeYieldPct = (perf.FeesEarnedUsd / perf.InitialValueUsd) * 100
	}

	outcome := "bad"
	if perf.PnlPct >= 5 || (perf.PnlPct >= 0 && feeYieldPct >= 2) {
		outcome = "good"
	} else if perf.PnlPct >= 0 {
		outcome = "neutral"
	} else if perf.PnlPct >= -5 {
		outcome = "poor"
	}

	if outcome == "neutral" {
		return nil
	}

	context := fmt.Sprintf("%s, strategy=%s, bin_step=%d, volatility=%.1f, fee_tvl_ratio=%.3f, organic=%.1f, bin_range=%v",
		perf.PoolName, perf.Strategy, perf.BinStep, perf.Volatility, perf.FeeTvlRatio, perf.OrganicScore, perf.BinRange)

	rule := ""
	if outcome == "good" || outcome == "bad" {
		if perf.RangeEfficiency < 30 && outcome == "bad" {
			rule = fmt.Sprintf("AVOID: %s-type pools (volatility=%.1f, bin_step=%d) with strategy=\"%s\" — went OOR %.1f%% of the time. Consider wider bin_range or bid_ask strategy.", perf.PoolName, perf.Volatility, perf.BinStep, perf.Strategy, 100-perf.RangeEfficiency)
			tags = append(tags, "oor", perf.Strategy, fmt.Sprintf("volatility_%.0f", perf.Volatility))
		} else if perf.RangeEfficiency > 80 && outcome == "good" {
			rule = fmt.Sprintf("PREFER: %s-type pools (volatility=%.1f, bin_step=%d) with strategy=\"%s\" — %.1f%% in-range efficiency, PnL +%.1f%%.", perf.PoolName, perf.Volatility, perf.BinStep, perf.Strategy, perf.RangeEfficiency, perf.PnlPct)
			tags = append(tags, "efficient", perf.Strategy)
		} else if outcome == "bad" && strings.Contains(strings.ToLower(perf.CloseReason), "volume") {
			rule = fmt.Sprintf("AVOID: Pools with fee_tvl_ratio=%.3f that showed volume collapse — fees evaporated quickly. Minimum sustained volume check needed before deploying.", perf.FeeTvlRatio)
			tags = append(tags, "volume_collapse")
		} else if outcome == "good" {
			rule = fmt.Sprintf("WORKED: %s → PnL +%.1f%%, range efficiency %.1f%%.", context, perf.PnlPct, perf.RangeEfficiency)
			tags = append(tags, "worked")
		} else {
			rule = fmt.Sprintf("FAILED: %s → PnL %.1f%%, range efficiency %.1f%%. Reason: %s.", context, perf.PnlPct, perf.RangeEfficiency, perf.CloseReason)
			tags = append(tags, "failed")
		}
	}

	if rule == "" {
		return nil
	}

	confidence := 0.35
	closeReasonText := strings.ToLower(perf.CloseReason)
	positiveEvidence := feeYieldPct >= 1 || perf.FeesEarnedUsd >= 3 || perf.PnlPct >= 3
	negativeEvidence := perf.PnlPct <= -5 || perf.RangeEfficiency <= 30 ||
		strings.Contains(closeReasonText, "out of range") ||
		strings.Contains(closeReasonText, "oor") ||
		strings.Contains(closeReasonText, "low yield") ||
		strings.Contains(closeReasonText, "volume")

	if outcome == "good" {
		if positiveEvidence {
			confidence = 0.82
		} else {
			confidence = 0.22
		}
	} else if outcome == "bad" {
		if negativeEvidence {
			confidence = 0.88
		} else {
			confidence = 0.45
		}
	} else if outcome == "poor" {
		if negativeEvidence {
			confidence = 0.68
		} else {
			confidence = 0.32
		}
	}

	return &Lesson{
		ID:              time.Now().UnixMilli(),
		Rule:            rule,
		Tags:            tags,
		Outcome:         outcome,
		SourceType:      "performance",
		Confidence:      confidence,
		Context:         context,
		PnlPct:          perf.PnlPct,
		FeesEarnedUsd:   perf.FeesEarnedUsd,
		InitialValueUsd: perf.InitialValueUsd,
		RangeEfficiency: perf.RangeEfficiency,
		CloseReason:     perf.CloseReason,
		Pool:            perf.Pool,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
}

func sanitizeLessonText(text string) string {
	cleaned := strings.TrimSpace(text)
	if len(cleaned) > maxManualLessonLen {
		cleaned = cleaned[:maxManualLessonLen]
	}
	return cleaned
}

func AddLesson(rule string, tags []string, pinned bool, role string) {
	safeRule := sanitizeLessonText(rule)
	if safeRule == "" {
		return
	}

	sourceType := "manual"
	for _, t := range tags {
		if t == "self_tune" || t == "config_change" {
			sourceType = "config_change"
			break
		}
	}

	ld := loadLessonsData()
	lesson := Lesson{
		ID:         time.Now().UnixMilli(),
		Rule:       safeRule,
		Tags:       tags,
		Outcome:    "manual",
		SourceType: sourceType,
		Pinned:     pinned,
		Role:       role,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	ld.Lessons = append(ld.Lessons, lesson)
	saveLessonsData(ld)

	logger.Log("lessons", fmt.Sprintf("Manual lesson added: %s", safeRule))
	// ignoring hivemind push for now
}

func GetLessonsForPrompt(agentType string, maxLessons int) string {
	ld := loadLessonsData()
	if len(ld.Lessons) == 0 {
		return ""
	}

	if maxLessons == 0 {
		maxLessons = 35
	}

	isAutoCycle := agentType == "SCREENER" || agentType == "MANAGER"
	pinnedCap := 10
	if isAutoCycle {
		pinnedCap = 5
	}
	roleCap := 15
	if isAutoCycle {
		roleCap = 6
	}
	recentCap := maxLessons

	outcomePriority := map[string]int{"bad": 0, "poor": 1, "failed": 1, "good": 2, "worked": 2, "manual": 1, "neutral": 3, "evolution": 2}

	byPriority := func(i, j int, list []Lesson) bool {
		pi := outcomePriority[list[i].Outcome]
		pj := outcomePriority[list[j].Outcome]
		if pi == 0 {
			pi = 3
		}
		if pj == 0 {
			pj = 3
		}
		return pi < pj
	}

	var pinned []Lesson
	for _, l := range ld.Lessons {
		if l.Pinned && (l.Role == "" || l.Role == agentType || agentType == "GENERAL") {
			pinned = append(pinned, l)
		}
	}
	sort.Slice(pinned, func(i, j int) bool { return byPriority(i, j, pinned) })
	if len(pinned) > pinnedCap {
		pinned = pinned[:pinnedCap]
	}

	usedIds := make(map[int64]bool)
	for _, l := range pinned {
		usedIds[l.ID] = true
	}

	roleTags := map[string][]string{
		"SCREENER": {"screening", "narrative", "strategy", "deployment", "token", "volume", "entry", "bundler", "holders", "organic"},
		"MANAGER":  {"management", "risk", "oor", "fees", "position", "hold", "close", "pnl", "rebalance", "claim"},
		"GENERAL":  {},
	}[agentType]

	var roleMatched []Lesson
	for _, l := range ld.Lessons {
		if usedIds[l.ID] {
			continue
		}
		roleOk := l.Role == "" || l.Role == agentType || agentType == "GENERAL"
		tagOk := len(roleTags) == 0 || len(l.Tags) == 0
		if !tagOk {
			for _, rt := range roleTags {
				for _, t := range l.Tags {
					if t == rt {
						tagOk = true
						break
					}
				}
				if tagOk {
					break
				}
			}
		}
		if roleOk && tagOk {
			roleMatched = append(roleMatched, l)
		}
	}
	sort.Slice(roleMatched, func(i, j int) bool { return byPriority(i, j, roleMatched) })
	if len(roleMatched) > roleCap {
		roleMatched = roleMatched[:roleCap]
	}
	for _, l := range roleMatched {
		usedIds[l.ID] = true
	}

	remBudget := recentCap - len(pinned) - len(roleMatched)
	var recent []Lesson
	if remBudget > 0 {
		for _, l := range ld.Lessons {
			if !usedIds[l.ID] {
				recent = append(recent, l)
			}
		}
		sort.Slice(recent, func(i, j int) bool {
			return recent[j].CreatedAt < recent[i].CreatedAt
		})
		if len(recent) > remBudget {
			recent = recent[:remBudget]
		}
	}

	if len(pinned) == 0 && len(roleMatched) == 0 && len(recent) == 0 {
		return ""
	}

	var sections []string
	if len(pinned) > 0 {
		sections = append(sections, fmt.Sprintf("── PINNED (%d) ──\n%s", len(pinned), fmtLessons(pinned)))
	}
	if len(roleMatched) > 0 {
		sections = append(sections, fmt.Sprintf("── %s (%d) ──\n%s", agentType, len(roleMatched), fmtLessons(roleMatched)))
	}
	if len(recent) > 0 {
		sections = append(sections, fmt.Sprintf("── RECENT (%d) ──\n%s", len(recent), fmtLessons(recent)))
	}

	shared := hivemind.GetSharedLessonsForPrompt(agentType, 4)
	if shared != "" {
		sections = append(sections, fmt.Sprintf("── HIVEMIND ──\n%s", shared))
	}

	return strings.Join(sections, "\n\n")
}

func fmtLessons(list []Lesson) string {
	var lines []string
	for _, l := range list {
		date := "unknown"
		if len(l.CreatedAt) >= 16 {
			date = strings.Replace(l.CreatedAt[:16], "T", " ", 1)
		}
		pin := ""
		if l.Pinned {
			pin = "📌 "
		}
		lines = append(lines, fmt.Sprintf("%s[%s] [%s] %s", pin, strings.ToUpper(l.Outcome), date, l.Rule))
	}
	return strings.Join(lines, "\n")
}

func evolveThresholds(perfData []PerformanceRecord, cfg *config.Config) {
	// Simplified threshold evolution for now
}

type PerformanceSummary struct {
	TotalPositionsClosed  int     `json:"total_positions_closed"`
	TotalPnlUsd           float64 `json:"total_pnl_usd"`
	AvgPnlPct             float64 `json:"avg_pnl_pct"`
	AvgRangeEfficiencyPct float64 `json:"avg_range_efficiency_pct"`
	WinRatePct            int     `json:"win_rate_pct"`
	TotalLessons          int     `json:"total_lessons"`
}

func GetPerformanceSummary() *PerformanceSummary {
	ld := loadLessonsData()
	if len(ld.Performance) == 0 {
		return nil
	}

	var totalPnl, sumPnlPct, sumEff float64
	wins := 0
	for _, p := range ld.Performance {
		totalPnl += p.PnlUsd
		sumPnlPct += p.PnlPct
		sumEff += p.RangeEfficiency
		if p.PnlUsd > 0 {
			wins++
		}
	}

	return &PerformanceSummary{
		TotalPositionsClosed:  len(ld.Performance),
		TotalPnlUsd:           math.Round(totalPnl*10000) / 10000,
		AvgPnlPct:             math.Round((sumPnlPct/float64(len(ld.Performance)))*100) / 100,
		AvgRangeEfficiencyPct: math.Round((sumEff/float64(len(ld.Performance)))*10) / 10,
		WinRatePct:            int((float64(wins) / float64(len(ld.Performance))) * 100),
		TotalLessons:          len(ld.Lessons),
	}
}
