package orchestrator

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	"meridian-go-rewrite/internal/agent"
	"meridian-go-rewrite/internal/config"
	"meridian-go-rewrite/internal/logger"
	"meridian-go-rewrite/internal/solana"
	"meridian-go-rewrite/internal/solana/dlmm"
	"meridian-go-rewrite/internal/telegram"
)

var (
	managementBusy uint32
	screeningBusy  uint32
	cronTasks      []*cron.Cron
	pnlPollStop    chan struct{}
)

var (
	ManagementLastRun time.Time
	ScreeningLastRun  time.Time
)

func StartCronJobs(cfg *config.Config) {
	StopCronJobs()

	tasks := cron.New(cron.WithSeconds())

	mgmtInterval := cfg.Schedule.ManagementIntervalMin
	tasks.AddFunc(fmt.Sprintf("@every %dm", mgmtInterval), func() {
		if atomic.LoadUint32(&managementBusy) == 1 {
			return
		}
		atomic.StoreUint32(&managementBusy, 1)
		ManagementLastRun = time.Now()
		defer atomic.StoreUint32(&managementBusy, 0)
		logger.Log("cron", "Starting management cycle")
		runManagementCycle(cfg, false)
	})

	screenInterval := cfg.Schedule.ScreeningIntervalMin
	tasks.AddFunc(fmt.Sprintf("@every %dm", screenInterval), func() {
		if atomic.LoadUint32(&screeningBusy) == 1 {
			return
		}
		atomic.StoreUint32(&screeningBusy, 1)
		ScreeningLastRun = time.Now()
		defer atomic.StoreUint32(&screeningBusy, 0)
		logger.Log("cron", "Starting screening cycle")
		runScreeningCycle(cfg, false)
	})

	healthInterval := cfg.Schedule.HealthCheckIntervalMin
	tasks.AddFunc(fmt.Sprintf("@every %dm", healthInterval), func() {
		if atomic.LoadUint32(&managementBusy) == 1 {
			return
		}
		atomic.StoreUint32(&managementBusy, 1)
		defer atomic.StoreUint32(&managementBusy, 0)
		logger.Log("cron", "Starting health check")
		agent.AgentLoop("\nHEALTH CHECK\nSummarize the current portfolio health and total fees earned.\n", cfg.LLM.MaxSteps, nil, "MANAGER", "", 0, false, nil)
	})

	tasks.AddFunc("0 1 * * *", func() {
		logger.Log("cron", "Starting morning briefing")
		runBriefing(cfg)
	})

	tasks.Start()
	cronTasks = append(cronTasks, tasks)

	pnlPollInterval := time.Duration(cfg.Schedule.PnlPollIntervalSec) * time.Second
	pnlPollStop = make(chan struct{})
	go func() {
		ticker := time.NewTicker(pnlPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if atomic.LoadUint32(&managementBusy) == 1 || atomic.LoadUint32(&screeningBusy) == 1 {
					continue
				}
				pollPnL(cfg)
			case <-pnlPollStop:
				return
			}
		}
	}()

	logger.Log("cron", fmt.Sprintf("Cycles started — management %dm, screening %dm, health %dm, PnL poll %ds",
		mgmtInterval, screenInterval, healthInterval, cfg.Schedule.PnlPollIntervalSec))
}

func StopCronJobs() {
	for _, c := range cronTasks {
		c.Stop()
	}
	cronTasks = nil
	if pnlPollStop != nil {
		close(pnlPollStop)
		pnlPollStop = nil
	}
}

func runManagementCycle(cfg *config.Config, silent bool) {
	logger.Log("cron", "Management cycle — position evaluation")

	walletAddr := cfg.WalletAddress()
	if walletAddr == "" {
		logger.Log("cron", "Management skipped — wallet not configured")
		return
	}

	client := dlmm.NewClient(walletAddr, cfg.RPCURLOrDefault())
	positionsResult, err := client.GetMyPositions(false)
	if err != nil {
		logger.Error("cron", fmt.Errorf("management pre-check failed: %w", err))
		if !silent && telegram.IsEnabled() {
			telegram.SendHTML("⚠️ <b>Management Cycle Failed</b>\nFailed to fetch positions: " + err.Error())
		}
		return
	}

	positions := positionsResult.Positions

	// 1. If 0 positions, skip LLM, trigger screener
	if len(positions) == 0 {
		logger.Log("cron", "Management skipped — no active positions to manage")
		if !silent && telegram.IsEnabled() {
			telegram.SendHTML("💼 <b>Management Cycle Skipped</b>\nNo active positions to manage.")
		}
		// Asynchronously trigger screening cycle
		go runScreeningCycle(cfg, silent)
		return
	}

	// 2. Deterministic rules check to skip LLM if everything is STAY
	needsAction := false
	var reason string
	for _, p := range positions {
		// Custom instructions -> always needs LLM
		if p.Instruction != nil && *p.Instruction != "" {
			needsAction = true
			reason = fmt.Sprintf("custom instruction on %s", p.Pair)
			break
		}
		// Stop Loss
		if p.PnLPct <= cfg.Management.StopLossPct {
			needsAction = true
			reason = fmt.Sprintf("stop loss on %s (%.2f%% <= %.2f%%)", p.Pair, p.PnLPct, cfg.Management.StopLossPct)
			break
		}
		// Take Profit
		if p.PnLPct >= cfg.Management.TakeProfitPct {
			needsAction = true
			reason = fmt.Sprintf("take profit on %s (%.2f%% >= %.2f%%)", p.Pair, p.PnLPct, cfg.Management.TakeProfitPct)
			break
		}
		// Out of Range for too long
		if p.MinutesOutOfRange != nil && *p.MinutesOutOfRange >= cfg.Management.OutOfRangeWaitMinutes {
			needsAction = true
			reason = fmt.Sprintf("%s out of range for %d minutes (limit: %d)", p.Pair, *p.MinutesOutOfRange, cfg.Management.OutOfRangeWaitMinutes)
			break
		}
		// Minimum Claim threshold
		if p.UnclaimedFeesUSD >= cfg.Management.MinClaimAmount {
			needsAction = true
			reason = fmt.Sprintf("claimable fees on %s ($%.4f >= $%.4f)", p.Pair, p.UnclaimedFeesUSD, cfg.Management.MinClaimAmount)
			break
		}
	}

	if !needsAction {
		logger.Log("cron", "Management skipped — all positions STAY")
		if !silent && telegram.IsEnabled() {
			var sb strings.Builder
			sb.WriteString("💼 <b>Management Cycle — All Healthy</b>\n\n")
			for _, p := range positions {
				inRangeStr := "🟢 In Range"
				if !p.InRange {
					oorMin := 0
					if p.MinutesOutOfRange != nil {
						oorMin = *p.MinutesOutOfRange
					}
					inRangeStr = fmt.Sprintf("🔴 Out of Range (%dm)", oorMin)
				}
				sb.WriteString(fmt.Sprintf("• <b>%s</b>\n"+
					"  • Value: <code>$%.4f</code>\n"+
					"  • Fees: <code>$%.4f</code>\n"+
					"  • PnL: <code>%.2f%%</code>\n"+
					"  • Status: %s\n\n",
					p.Pair, p.TotalValueUSD, p.UnclaimedFeesUSD, p.PnLPct, inRangeStr))
			}
			telegram.SendHTML(sb.String())
		}
		return
	}

	logger.Log("cron", "Management action required — invoking LLM ("+reason+")")

	var callbacks *agent.ToolCallbacks
	var lm *telegram.LiveMessage

	if !silent && telegram.IsEnabled() {
		lm, err = telegram.CreateLiveMessage("💼 <b>Management Cycle</b>", "Action required: "+reason+". Invoking LLM...")
		if err == nil && lm != nil {
			callbacks = &agent.ToolCallbacks{
				OnToolStart: func(name string, args map[string]any) {
					lm.ToolStart(name)
				},
				OnToolFinish: func(name string, result any, success bool) {
					lm.ToolFinish(name, result, success)
				},
			}
		}
	}

	result, err := agent.AgentLoop(
		`MANAGEMENT CYCLE
You have open positions to evaluate. For each position:
- If there's a CLOSE action: call close_position with the rule name as the reason.
- If there's a CLAIM action: call claim_fees.
- If STAY: no action needed — report that all positions are healthy.
First call get_my_positions to check current state, then act accordingly.
Be concise — one line per action.
`,
		cfg.LLM.MaxSteps, nil, "MANAGER", "", 0, false, callbacks)
	if err != nil {
		logger.Error("cron", err)
		if lm != nil {
			lm.Fail(err.Error())
		}
		return
	}
	logger.Log("cron", "Management cycle complete")
	if lm != nil {
		lm.Finalize(result.Content)
	}
}

func runScreeningCycle(cfg *config.Config, silent bool) {
	logger.Log("cron", "Screening cycle — pool discovery")

	// Hard guards — don't even run the agent if preconditions aren't met
	walletAddr := cfg.WalletAddress()
	if walletAddr != "" {
		// Position count check
		client := dlmm.NewClient(walletAddr, cfg.RPCURLOrDefault())
		positions, err := client.GetMyPositions(true)
		if err == nil && len(positions.Positions) >= cfg.Risk.MaxPositions {
			logger.Log("cron", fmt.Sprintf("Screening skipped — max positions reached (%d/%d)", len(positions.Positions), cfg.Risk.MaxPositions))
			if !silent && telegram.IsEnabled() {
				telegram.SendHTML(fmt.Sprintf("ℹ️ <b>Screening Cycle Skipped</b>\nMax positions reached (%d/%d)", len(positions.Positions), cfg.Risk.MaxPositions))
			}
			return
		}

		// Balance check
		minRequired := config.ComputeMinOpenBalance(cfg)
		if !cfg.DryRun {
			balances, err := solana.GetHeliusBalances(walletAddr, cfg.Tokens.SOL, cfg.Tokens.USDC)
			if err != nil {
				logger.Error("cron", fmt.Errorf("screening balance check failed: %w", err))
				return
			}
			if balances.SOL < minRequired {
				logger.Log("cron", fmt.Sprintf("Screening skipped — insufficient SOL (%.3f < %.3f minimum)", balances.SOL, minRequired))
				if !silent && telegram.IsEnabled() {
					telegram.SendHTML(fmt.Sprintf("ℹ️ <b>Screening Cycle Skipped</b>\nInsufficient SOL (%.3f < %.3f minimum)", balances.SOL, minRequired))
				}
				return
			}
		}
	}

	var callbacks *agent.ToolCallbacks
	var lm *telegram.LiveMessage
	var err error

	if !silent && telegram.IsEnabled() {
		lm, err = telegram.CreateLiveMessage("🔍 <b>Screening Cycle</b>", "Discovering pools and evaluating candidate yield...")
		if err == nil && lm != nil {
			callbacks = &agent.ToolCallbacks{
				OnToolStart: func(name string, args map[string]any) {
					lm.ToolStart(name)
				},
				OnToolFinish: func(name string, result any, success bool) {
					lm.ToolFinish(name, result, success)
				},
			}
		}
	}

	goal := fmt.Sprintf(`SCREENING CYCLE
Find and deploy into the best candidate.
Call get_top_candidates(limit=10) first, then evaluate candidates.
If one is clearly worth it, call deploy_position with:
- amount_y = compute_deploy_amount from wallet
- strategy = the active strategy or "bid_ask"
- bins_below based on volatility (round(%d + (volatility/5) × %d))
- bins_above = 0
- pool_name, base_mint, bin_step, volatility, fee_tvl_ratio, organic_score from the candidate
If no pool qualifies, report ⛔ NO DEPLOY with clear reasoning.
If deploy succeeds, report 🚀 DEPLOYED with range and pool metrics.
`,
		cfg.Strategy.MinBinsBelow, cfg.Strategy.MaxBinsBelow-cfg.Strategy.MinBinsBelow)

	result, err := agent.AgentLoop(goal, cfg.LLM.MaxSteps, nil, "SCREENER", "", 0, false, callbacks)
	if err != nil {
		logger.Error("cron", err)
		if lm != nil {
			lm.Fail(err.Error())
		}
		return
	}
	logger.Log("cron", "Screening cycle complete")
	if lm != nil {
		lm.Finalize(result.Content)
	}
}

func runBriefing(cfg *config.Config) {
	goal := `MORNING BRIEFING
Generate a morning briefing for the last 24h. Include:
- Positions opened and closed
- Net PnL and fees earned
- Win rate
- Current portfolio status (open positions count)
Format as clean HTML with <b> for section headers. Keep it under 4000 chars for Telegram.
`
	result, err := agent.AgentLoop(goal, cfg.LLM.MaxSteps, nil, "GENERAL", "", 2048, false, nil)
	if err != nil {
		logger.Error("briefing", err)
		return
	}
	logger.Log("briefing", "Briefing generated")
	if telegram.IsEnabled() {
		telegram.SendHTML(result.Content)
	}
}

func pollPnL(cfg *config.Config) {
	logger.Log("pnl", "PnL poll tick")
}

func NextRunIn(lastRun time.Time, intervalMin int) string {
	if lastRun.IsZero() {
		return "now"
	}
	elapsed := time.Since(lastRun)
	remaining := time.Duration(intervalMin)*time.Minute - elapsed
	if remaining <= 0 {
		return "now"
	}
	m := int(remaining.Minutes())
	s := int(remaining.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func ManagementBusy() bool {
	return atomic.LoadUint32(&managementBusy) == 1
}

func ScreeningBusy() bool {
	return atomic.LoadUint32(&screeningBusy) == 1
}
