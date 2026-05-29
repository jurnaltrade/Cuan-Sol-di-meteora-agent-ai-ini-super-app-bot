package agentmeridian

import (
	"fmt"
	"strings"
	"time"

	"meridian-go-rewrite/internal/persistence"
)

func GenerateBriefing() string {
	stateData := persistence.GetState()
	ld := loadLessonsData()

	now := time.Now()
	last24h := now.Add(-24 * time.Hour)

	var openedLast24h []persistence.PositionState
	var closedLast24h []persistence.PositionState
	var openPositions []persistence.PositionState

	for _, p := range stateData.Positions {
		if !p.Closed {
			openPositions = append(openPositions, p)
		}

		t, err := time.Parse(time.RFC3339, p.DeployedAt)
		if err == nil && t.After(last24h) {
			openedLast24h = append(openedLast24h, p)
		}

		if p.Closed {
			t, err := time.Parse(time.RFC3339, p.ClosedAt)
			if err == nil && t.After(last24h) {
				closedLast24h = append(closedLast24h, p)
			}
		}
	}

	var totalPnLUsd float64
	var totalFeesUsd float64
	var perfLast24h []PerformanceRecord

	for _, p := range ld.Performance {
		t, err := time.Parse(time.RFC3339, p.RecordedAt)
		if err == nil && t.After(last24h) {
			perfLast24h = append(perfLast24h, p)
			totalPnLUsd += p.PnlUsd
			totalFeesUsd += p.FeesEarnedUsd
		}
	}

	var lessonsLast24h []Lesson
	for _, l := range ld.Lessons {
		t, err := time.Parse(time.RFC3339, l.CreatedAt)
		if err == nil && t.After(last24h) {
			lessonsLast24h = append(lessonsLast24h, l)
		}
	}

	perfSummary := GetPerformanceSummary()

	var lines []string
	lines = append(lines, "☀️ <b>Morning Briefing</b> (Last 24h)")
	lines = append(lines, "────────────────")
	lines = append(lines, "<b>Activity:</b>")
	lines = append(lines, fmt.Sprintf("📥 Positions Opened: %d", len(openedLast24h)))
	lines = append(lines, fmt.Sprintf("📤 Positions Closed: %d", len(closedLast24h)))
	lines = append(lines, "")
	lines = append(lines, "<b>Performance:</b>")

	sign := ""
	if totalPnLUsd >= 0 {
		sign = "+"
	}
	lines = append(lines, fmt.Sprintf("💰 Net PnL: %s$%.4f", sign, totalPnLUsd))
	lines = append(lines, fmt.Sprintf("💎 Fees Earned: $%.4f", totalFeesUsd))

	if len(perfLast24h) > 0 {
		wins := 0
		for _, p := range perfLast24h {
			if p.PnlUsd > 0 {
				wins++
			}
		}
		winRate := int(float64(wins) / float64(len(perfLast24h)) * 100)
		lines = append(lines, fmt.Sprintf("📈 Win Rate (24h): %d%%", winRate))
	} else {
		lines = append(lines, "📈 Win Rate (24h): N/A")
	}

	lines = append(lines, "")
	lines = append(lines, "<b>Lessons Learned:</b>")
	if len(lessonsLast24h) > 0 {
		for _, l := range lessonsLast24h {
			lines = append(lines, "• "+l.Rule)
		}
	} else {
		lines = append(lines, "• No new lessons recorded overnight.")
	}

	lines = append(lines, "")
	lines = append(lines, "<b>Current Portfolio:</b>")
	lines = append(lines, fmt.Sprintf("📂 Open Positions: %d", len(openPositions)))

	if perfSummary != nil {
		lines = append(lines, fmt.Sprintf("📊 All-time PnL: $%.4f (%d%% win)", perfSummary.TotalPnlUsd, perfSummary.WinRatePct))
	}

	lines = append(lines, "────────────────")
	return strings.Join(lines, "\n")
}
