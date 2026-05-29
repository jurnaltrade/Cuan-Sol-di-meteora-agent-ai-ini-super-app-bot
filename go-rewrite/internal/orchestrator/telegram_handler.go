package orchestrator

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"meridian-go-rewrite/internal/agent"
	"meridian-go-rewrite/internal/config"
	"meridian-go-rewrite/internal/hivemind"
	"meridian-go-rewrite/internal/logger"
	"meridian-go-rewrite/internal/registry"
	"meridian-go-rewrite/internal/screening"
	"meridian-go-rewrite/internal/solana"
	"meridian-go-rewrite/internal/solana/dlmm"
	"meridian-go-rewrite/internal/solana/types"
	"meridian-go-rewrite/internal/telegram"
)

var (
	latestCandidates      []screening.Candidate
	latestCandidatesMutex sync.Mutex
	CronRunning           bool = true
)

// StartTelegramBot initializes and starts the Telegram bot update polling.
func StartTelegramBot(cfg *config.Config) {
	if cfg.Telegram.BotToken == "" {
		logger.Log("telegram", "Telegram Bot Token is empty, skipping polling")
		return
	}

	err := telegram.StartPolling(cfg, func(msg *tgbotapi.Message) {
		handleTelegramMessage(cfg, msg)
	})
	if err != nil {
		logger.Error("telegram", fmt.Errorf("failed to start Telegram bot: %w", err))
	} else {
		logger.Log("telegram", "Telegram bot listener started successfully")
	}
}

func handleTelegramMessage(cfg *config.Config, msg *tgbotapi.Message) {
	if msg == nil {
		return
	}

	// 1. Authorization check
	if len(cfg.Telegram.AllowedUsers) > 0 {
		allowed := false
		senderUsername := msg.From.UserName
		senderID := fmt.Sprintf("%d", msg.From.ID)
		for _, u := range cfg.Telegram.AllowedUsers {
			if strings.EqualFold(u, senderUsername) || u == senderID {
				allowed = true
				break
			}
		}
		if !allowed {
			logger.Log("telegram", fmt.Sprintf("Unauthorized message from %s (%d)", senderUsername, msg.From.ID))
			return
		}
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	// 2. Command routing
	if strings.HasPrefix(text, "/") {
		cmd := strings.Split(text, " ")[0]
		switch cmd {
		case "/start", "/help":
			helpMsg := "🤖 <b>Meridian Telegram Commands</b> 🤖\n\n" +
				"• <b>/help</b> — show commands\n" +
				"• <b>/bind</b> — bind this private chat as operator chat\n" +
				"• <b>/status</b> — wallet + positions snapshot\n" +
				"• <b>/whoami</b> — show Telegram sender/chat identity\n" +
				"• <b>/wallet</b> — wallet, deploy amount, HiveMind status\n" +
				"• <b>/positions</b> — list open positions\n" +
				"• <b>/pool &lt;n&gt;</b> — detailed info for one open position\n" +
				"• <b>/close &lt;n&gt;</b> — close one position by index\n" +
				"• <b>/closeall</b> — close all open positions\n" +
				"• <b>/set &lt;n&gt; &lt;note&gt;</b> — set note/instruction on position\n" +
				"• <b>/config</b> — show important runtime config\n" +
				"• <b>/settings</b> — view all config keys &amp; current values\n" +
				"• <b>/setcfg &lt;key&gt; &lt;value&gt;</b> — update config (use '/setcfg' or '/setcfg help' to see all valid keys)\n" +
				"• <b>/screen</b> — refresh deterministic candidate list\n" +
				"• <b>/candidates</b> — show latest cached candidates\n" +
				"• <b>/deploy &lt;n&gt;</b> — deploy candidate by cached index\n" +
				"• <b>/briefing</b> — morning briefing\n" +
				"• <b>/hive</b> — HiveMind sync status\n" +
				"• <b>/hive pull</b> — manual HiveMind pull now\n" +
				"• <b>/pause</b> — stop cron cycles\n" +
				"• <b>/resume</b> — start cron cycles again\n" +
				"• <b>/stop</b> — shut down agent"
			telegram.SendHTMLToChat(msg.Chat.ID, helpMsg)

		case "/bind":
			senderId := msg.From.ID
			senderUsername := "(no username)"
			if msg.From.UserName != "" {
				senderUsername = "@" + msg.From.UserName
			}
			boundChatId := msg.Chat.ID
			cfg.Telegram.ChatID = fmt.Sprintf("%d", boundChatId)
			err := config.SaveConfig(cfg)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Failed to persist bind configuration: "+err.Error())
				return
			}
			telegram.SendHTMLToChat(msg.Chat.ID, fmt.Sprintf("✅ <b>Bound this chat.</b>\n• <b>chat_id</b>: <code>%d</code>\n• <b>from.id</b>: <code>%d</code>\n• <b>from.username</b>: %s", boundChatId, senderId, senderUsername))

		case "/whoami":
			senderId := msg.From.ID
			senderUsername := "(no username)"
			if msg.From.UserName != "" {
				senderUsername = "@" + msg.From.UserName
			}
			boundChatId := msg.Chat.ID
			chatType := msg.Chat.Type
			telegram.SendMessageToChat(msg.Chat.ID, fmt.Sprintf("chat_id: %d\nchat_type: %s\nfrom.id: %d\nfrom.username: %s", boundChatId, chatType, senderId, senderUsername))

		case "/wallet":
			status, err := formatWalletStatus(cfg)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error fetching wallet status: "+err.Error())
				return
			}
			telegram.SendMessageToChat(msg.Chat.ID, status)

		case "/status":
			status, err := formatWalletStatus(cfg)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error fetching status: "+err.Error())
				return
			}
			
			mode := "LIVE"
			if cfg.DryRun {
				mode = "DRY RUN"
			}
			
			cyclesStr := fmt.Sprintf(
				"\n\n⚙️ <b>Orchestrator Cycles</b>\n"+
				"• <b>Mode</b>: %s\n"+
				"• <b>Management Cycle</b>: %dm (Next in: %s)\n"+
				"• <b>Screening Cycle</b>: %dm (Next in: %s)\n"+
				"• <b>Strategy</b>: %s",
				mode,
				cfg.Schedule.ManagementIntervalMin,
				NextRunIn(ManagementLastRun, cfg.Schedule.ManagementIntervalMin),
				cfg.Schedule.ScreeningIntervalMin,
				NextRunIn(ScreeningLastRun, cfg.Schedule.ScreeningIntervalMin),
				cfg.Strategy.Strategy,
			)

			suffix := ""
			walletAddr := cfg.WalletAddress()
			if walletAddr != "" {
				client := dlmm.NewClient(walletAddr, cfg.RPCURLOrDefault())
				positions, err := client.GetMyPositions(true)
				if err == nil && len(positions.Positions) > 0 {
					suffix = "\n\nUse /positions for the numbered list."
				}
			}
			telegram.SendHTMLToChat(msg.Chat.ID, status+cyclesStr+suffix)

		case "/balance":
			walletAddr := cfg.WalletAddress()
			if walletAddr == "" {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Wallet address not configured")
				return
			}
			balances, err := solana.GetHeliusBalances(walletAddr, cfg.Tokens.SOL, cfg.Tokens.USDC)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error fetching balances: "+err.Error())
				return
			}
			balMsg := fmt.Sprintf("💰 <b>Wallet Balances</b>\n\n"+
				"• <b>SOL</b>: %.6f ($%.4f)\n"+
				"• <b>USDC</b>: %.4f\n"+
				"• <b>Total</b>: $%.4f",
				balances.SOL,
				balances.SOL*balances.SOLPrice,
				balances.USDC,
				balances.TotalUSD,
			)
			telegram.SendHTMLToChat(msg.Chat.ID, balMsg)

		case "/positions":
			walletAddr := cfg.WalletAddress()
			if walletAddr == "" {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Wallet address not configured")
				return
			}
			client := dlmm.NewClient(walletAddr, cfg.RPCURLOrDefault())
			positions, err := client.GetMyPositions(true)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error fetching positions: "+err.Error())
				return
			}
			if len(positions.Positions) == 0 {
				telegram.SendMessageToChat(msg.Chat.ID, "ℹ️ No active DLMM positions found.")
				return
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("💼 <b>Active DLMM Positions (%d):</b>\n\n", len(positions.Positions)))
			for i, p := range positions.Positions {
				rangeStatus := "✅ In Range"
				if !p.InRange {
					rangeStatus = "🚨 Out of Range"
				}
				posShort := p.Position
				if len(posShort) > 8 {
					posShort = posShort[:8]
				}

				ageStr := "Unknown"
				if p.AgeMinutes != nil {
					mins := *p.AgeMinutes
					if mins < 60 {
						ageStr = fmt.Sprintf("%dm", mins)
					} else {
						hours := mins / 60
						days := hours / 24
						remHours := hours % 24
						remMins := mins % 60
						if days > 0 {
							ageStr = fmt.Sprintf("%dd %dh", days, remHours)
						} else {
							ageStr = fmt.Sprintf("%dh %dm", remHours, remMins)
						}
					}
				}

				sign := ""
				if p.PnLUSD > 0 {
					sign = "+"
				}

				sb.WriteString(fmt.Sprintf("%d. <b>%s</b>\n"+
					"• Position: <code>%s</code>\n"+
					"• Status: %s\n"+
					"• Age: <code>%s</code>\n"+
					"• Size: <code>$%.4f</code>\n"+
					"• PnL: <b>%s$%.4f (%s%.2f%%)</b>\n"+
					"• Fees: <code>$%.4f</code> ($%.4f claimed, $%.4f unclaimed)\n\n",
					i+1, p.Pair, posShort, rangeStatus, ageStr, p.TotalValueUSD, sign, p.PnLUSD, sign, p.PnLPct, p.CollectedFeesUSD+p.UnclaimedFeesUSD, p.CollectedFeesUSD, p.UnclaimedFeesUSD))
			}
			telegram.SendHTMLToChat(msg.Chat.ID, sb.String())

		case "/pool":
			parts := strings.Split(text, " ")
			if len(parts) < 2 {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Usage: /pool <index>")
				return
			}
			var idx int
			_, err := fmt.Sscanf(parts[1], "%d", &idx)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Invalid position index")
				return
			}
			idx = idx - 1

			walletAddr := cfg.WalletAddress()
			client := dlmm.NewClient(walletAddr, cfg.RPCURLOrDefault())
			positions, err := client.GetMyPositions(true)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error fetching positions: "+err.Error())
				return
			}
			if idx < 0 || idx >= len(positions.Positions) {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Invalid index. Use /positions first.")
				return
			}
			pos := positions.Positions[idx]

			oorStr := "IN RANGE"
			if !pos.InRange {
				oorMin := 0
				if pos.MinutesOutOfRange != nil {
					oorMin = *pos.MinutesOutOfRange
				}
				oorStr = fmt.Sprintf("OOR %dm", oorMin)
			}

			activeBinStr := "?"
			if pos.ActiveBin != nil {
				activeBinStr = fmt.Sprintf("%d", *pos.ActiveBin)
			}

			ageStr := "?"
			if pos.AgeMinutes != nil {
				ageStr = fmt.Sprintf("%dm", *pos.AgeMinutes)
			}

			noteStr := ""
			if pos.Instruction != nil && *pos.Instruction != "" {
				noteStr = fmt.Sprintf("\nNote: %s", *pos.Instruction)
			}

			msgText := fmt.Sprintf(
				"%d. %s\nPool: %s\nPosition: %s\nRange: %d → %d | active %s\nPnL: %.2f%% | fees: $%.4f\nValue: $%.4f\nAge: %s | %s%s",
				idx+1, pos.Pair, pos.Pool, pos.Position, pos.LowerBin, pos.UpperBin, activeBinStr,
				pos.PnLPct, pos.UnclaimedFeesUSD, pos.TotalValueUSD, ageStr, oorStr, noteStr,
			)
			telegram.SendMessageToChat(msg.Chat.ID, msgText)

		case "/close":
			parts := strings.Split(text, " ")
			if len(parts) < 2 {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Usage: /close <index>")
				return
			}
			var idx int
			_, err := fmt.Sscanf(parts[1], "%d", &idx)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Invalid position index")
				return
			}
			idx = idx - 1

			walletAddr := cfg.WalletAddress()
			client := dlmm.NewClient(walletAddr, cfg.RPCURLOrDefault())
			positions, err := client.GetMyPositions(true)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error: "+err.Error())
				return
			}
			if idx < 0 || idx >= len(positions.Positions) {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Invalid index. Use /positions first.")
				return
			}
			pos := positions.Positions[idx]
			telegram.SendMessageToChat(msg.Chat.ID, fmt.Sprintf("🔄 Closing %s...", pos.Pair))

			go func() {
				result, err := dlmm.ClosePosition(pos.Position, "Telegram close command", false, cfg)
				if err != nil {
					telegram.SendMessageToChat(msg.Chat.ID, "❌ Error closing position: "+err.Error())
					return
				}
				if result.Success {
					autoSwapStr := ""
					if result.AutoSwapped {
						autoSwapStr = fmt.Sprintf("\nAuto-swapped base token back to SOL (received %s SOL)", result.SolReceived)
					}
					telegram.SendMessageToChat(msg.Chat.ID, fmt.Sprintf("✅ Closed %s\nPnL: $%.2f (%.2f%%)%s", pos.Pair, result.PnLUSD, result.PnLPct, autoSwapStr))
				} else {
					telegram.SendMessageToChat(msg.Chat.ID, fmt.Sprintf("❌ Close failed: %s", result.Error))
				}
			}()

		case "/closeall":
			walletAddr := cfg.WalletAddress()
			client := dlmm.NewClient(walletAddr, cfg.RPCURLOrDefault())
			positions, err := client.GetMyPositions(true)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error: "+err.Error())
				return
			}
			if len(positions.Positions) == 0 {
				telegram.SendMessageToChat(msg.Chat.ID, "No open positions.")
				return
			}
			telegram.SendMessageToChat(msg.Chat.ID, fmt.Sprintf("🔄 Closing %d position(s)...", len(positions.Positions)))

			go func() {
				var results []string
				for _, pos := range positions.Positions {
					result, err := dlmm.ClosePosition(pos.Position, "Telegram closeall", false, cfg)
					if err != nil {
						results = append(results, fmt.Sprintf("• %s: failed (%v)", pos.Pair, err))
					} else if result.Success {
						results = append(results, fmt.Sprintf("• %s: closed, PnL $%.2f (%.2f%%)", pos.Pair, result.PnLUSD, result.PnLPct))
					} else {
						results = append(results, fmt.Sprintf("• %s: failed (%s)", pos.Pair, result.Error))
					}
				}
				telegram.SendMessageToChat(msg.Chat.ID, fmt.Sprintf("Close-all finished.\n\n%s", strings.Join(results, "\n")))
			}()

		case "/set":
			parts := strings.SplitN(text, " ", 3)
			if len(parts) < 3 {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Usage: /set <index> <note>")
				return
			}
			var idx int
			_, err := fmt.Sscanf(parts[1], "%d", &idx)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Invalid position index")
				return
			}
			idx = idx - 1
			note := strings.TrimSpace(parts[2])

			walletAddr := cfg.WalletAddress()
			client := dlmm.NewClient(walletAddr, cfg.RPCURLOrDefault())
			positions, err := client.GetMyPositions(true)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error: "+err.Error())
				return
			}
			if idx < 0 || idx >= len(positions.Positions) {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Invalid index. Use /positions first.")
				return
			}
			pos := positions.Positions[idx]

			err = SetPositionInstruction(pos.Position, note)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error setting note: "+err.Error())
				return
			}
			telegram.SendMessageToChat(msg.Chat.ID, fmt.Sprintf("✅ Note set for %s:\n\"%s\"", pos.Pair, note))

		case "/config":
			telegram.SendMessageToChat(msg.Chat.ID, formatConfigSnapshot(cfg))

		case "/settings", "/menu":
			settingsMsg := "⚙️ <b>Meridian Settings</b> ⚙️\n\n" +
				"You can update any of these configuration settings using the command:\n" +
				"<code>/setcfg &lt;key&gt; &lt;value&gt;</code>\n\n" +
				"<b>All Config Keys &amp; Current Values:</b>\n" +
				fmt.Sprintf("• <code>strategy</code>: <code>%s</code>\n", cfg.Strategy.Strategy) +
				fmt.Sprintf("• <code>minBinsBelow</code>: <code>%d</code>\n", cfg.Strategy.MinBinsBelow) +
				fmt.Sprintf("• <code>maxBinsBelow</code>: <code>%d</code>\n", cfg.Strategy.MaxBinsBelow) +
				fmt.Sprintf("• <code>defaultBinsBelow</code>: <code>%d</code>\n", cfg.Strategy.DefaultBinsBelow) +
				fmt.Sprintf("• <code>deployAmountSol</code>: <code>%.4f</code>\n", cfg.Management.DeployAmountSol) +
				fmt.Sprintf("• <code>maxDeployAmount</code>: <code>%.4f</code>\n", cfg.Risk.MaxDeployAmount) +
				fmt.Sprintf("• <code>minSolToOpen</code>: <code>%.4f</code>\n", cfg.Management.MinSolToOpen) +
				fmt.Sprintf("• <code>gasReserve</code>: <code>%.4f</code>\n", cfg.Management.GasReserve) +
				fmt.Sprintf("• <code>positionSizePct</code>: <code>%.2f</code> (%.0f%%)\n", cfg.Management.PositionSizePct, cfg.Management.PositionSizePct*100) +
				fmt.Sprintf("• <code>maxPositions</code>: <code>%d</code>\n", cfg.Risk.MaxPositions) +
				fmt.Sprintf("• <code>minTvl</code>: <code>%.0f</code>\n", cfg.Screening.MinTvl) +
				fmt.Sprintf("• <code>maxTvl</code>: <code>%.0f</code>\n", cfg.Screening.MaxTvl) +
				fmt.Sprintf("• <code>stopLossPct</code>: <code>%.1f</code>\n", cfg.Management.StopLossPct) +
				fmt.Sprintf("• <code>takeProfitPct</code>: <code>%.1f</code>\n", cfg.Management.TakeProfitPct) +
				fmt.Sprintf("• <code>managementIntervalMin</code>: <code>%d</code>\n", cfg.Schedule.ManagementIntervalMin) +
				fmt.Sprintf("• <code>screeningIntervalMin</code>: <code>%d</code>\n", cfg.Schedule.ScreeningIntervalMin) +
				fmt.Sprintf("• <code>dryRun</code>: <code>%t</code>\n\n", cfg.DryRun) +
				"<b>Example:</b> <code>/setcfg deployAmountSol 0.35</code>"
			telegram.SendHTMLToChat(msg.Chat.ID, settingsMsg)

		case "/setcfg":
			parts := strings.SplitN(text, " ", 3)
			if len(parts) < 3 || parts[1] == "help" || parts[1] == "?" {
				settingsMsg := "⚙️ <b>Meridian /setcfg Help</b> ⚙️\n\n" +
					"Use <code>/setcfg &lt;key&gt; &lt;value&gt;</code> to update configurations.\n\n" +
					"<b>Valid Config Keys:</b>\n" +
					"• <code>strategy</code> (string)\n" +
					"• <code>minBinsBelow</code> (int)\n" +
					"• <code>maxBinsBelow</code> (int)\n" +
					"• <code>defaultBinsBelow</code> (int)\n" +
					"• <code>deployAmountSol</code> (float)\n" +
					"• <code>maxDeployAmount</code> (float)\n" +
					"• <code>minSolToOpen</code> (float)\n" +
					"• <code>gasReserve</code> (float)\n" +
					"• <code>positionSizePct</code> (float, 0.0 to 1.0)\n" +
					"• <code>maxPositions</code> (int)\n" +
					"• <code>minTvl</code> (float)\n" +
					"• <code>maxTvl</code> (float)\n" +
					"• <code>stopLossPct</code> (float, e.g. -50)\n" +
					"• <code>takeProfitPct</code> (float, e.g. 5)\n" +
					"• <code>managementIntervalMin</code> (int)\n" +
					"• <code>screeningIntervalMin</code> (int)\n" +
					"• <code>dryRun</code> (bool, true/false)\n\n" +
					"<i>To view current settings, use the <code>/settings</code> command.</i>"
				telegram.SendHTMLToChat(msg.Chat.ID, settingsMsg)
				return
			}
			key := parts[1]
			valStr := parts[2]
			err := updateConfigValue(cfg, key, valStr)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error updating config: "+err.Error())
				return
			}
			config.Set(cfg)
			telegram.SendMessageToChat(msg.Chat.ID, fmt.Sprintf("✅ Updated %s = %s", key, valStr))

		case "/screen":
			telegram.SendMessageToChat(msg.Chat.ID, "🔍 Refreshing deterministic candidate list...")
			go func() {
				result, err := screening.DiscoverAndScore(cfg)
				if err != nil {
					telegram.SendMessageToChat(msg.Chat.ID, "❌ Error screening pools: "+err.Error())
					return
				}

				if registry.SignalWeights != nil {
					weights := registry.SignalWeights.GetWeights()
					for i := range result.Candidates {
						result.Candidates[i].ApplyWeights(weights)
					}
					sort.Slice(result.Candidates, func(i, j int) bool {
						return result.Candidates[i].WeightedScore > result.Candidates[j].WeightedScore
					})
				}

				limit := 5
				if len(result.Candidates) > limit {
					result.Candidates = result.Candidates[:limit]
				}

				latestCandidatesMutex.Lock()
				latestCandidates = result.Candidates
				latestCandidatesMutex.Unlock()

				if len(result.Candidates) == 0 {
					telegram.SendMessageToChat(msg.Chat.ID, "No pool candidates passed the screening criteria.")
					return
				}

				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("Top candidates (%d):\n\n", len(result.Candidates)))
				for i, c := range result.Candidates {
					sb.WriteString(fmt.Sprintf("%d. %s | %s\n   fee/aTVL %.2f%% | vol $%.0f | organic %.0f\n",
						i+1, c.Name, c.PoolAddress, c.FeeTVLRatio, c.Volume, c.OrganicScore))
				}
				telegram.SendMessageToChat(msg.Chat.ID, sb.String())
			}()

		case "/candidates":
			latestCandidatesMutex.Lock()
			candidates := latestCandidates
			latestCandidatesMutex.Unlock()

			if len(candidates) == 0 {
				telegram.SendMessageToChat(msg.Chat.ID, "No cached candidates available. Run /screen first.")
				return
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Latest candidates (%d):\n\n", len(candidates)))
			for i, c := range candidates {
				sb.WriteString(fmt.Sprintf("%d. %s | %s\n   fee/aTVL %.2f%% | vol $%.0f | organic %.0f\n",
					i+1, c.Name, c.PoolAddress, c.FeeTVLRatio, c.Volume, c.OrganicScore))
			}
			telegram.SendMessageToChat(msg.Chat.ID, sb.String())

		case "/deploy":
			parts := strings.Split(text, " ")
			if len(parts) < 2 {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Usage: /deploy <index>")
				return
			}
			var idx int
			_, err := fmt.Sscanf(parts[1], "%d", &idx)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Invalid candidate index")
				return
			}
			idx = idx - 1

			latestCandidatesMutex.Lock()
			candidates := latestCandidates
			latestCandidatesMutex.Unlock()

			if idx < 0 || idx >= len(candidates) {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Invalid index. Run /screen first.")
				return
			}
			c := candidates[idx]

			telegram.SendMessageToChat(msg.Chat.ID, fmt.Sprintf("🚀 Deploying into %s...", c.Name))

			walletAddr := cfg.WalletAddress()
			balances, err := solana.GetHeliusBalances(walletAddr, cfg.Tokens.SOL, cfg.Tokens.USDC)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error fetching balance: "+err.Error())
				return
			}

			deployAmount := config.ComputeDeployAmount(balances.SOL, cfg)

			lo := float64(cfg.Strategy.MinBinsBelow)
			hi := float64(cfg.Strategy.MaxBinsBelow)
			binsBelow := int(lo + (c.Volatility / 5.0) * (hi - lo))
			if binsBelow < int(lo) {
				binsBelow = int(lo)
			}
			if binsBelow > int(hi) {
				binsBelow = int(hi)
			}

			input := dlmm.DeployInput{
				PoolAddress:  c.PoolAddress,
				AmountY:      deployAmount,
				AmountX:      0,
				Strategy:     cfg.Strategy.Strategy,
				BinsBelow:    binsBelow,
				BinsAbove:    0,
				PoolName:     c.Name,
				BaseMint:     c.TokenXMint,
				BinStep:      c.BinStep,
				Volatility:   c.Volatility,
				FeeTVLRatio:  c.FeeTVLRatio,
				OrganicScore: c.OrganicScore,
			}

			go func() {
				result, err := dlmm.DeployPosition(input, cfg)
				if err != nil {
					telegram.SendMessageToChat(msg.Chat.ID, "❌ Deploy failed: "+err.Error())
					return
				}

				if result.Success {
					var bal float64
					if balances, err := solana.GetHeliusBalances(walletAddr, cfg.Tokens.SOL, cfg.Tokens.USDC); err == nil {
						bal = balances.SOL
					}

					telegram.NotifyDeploy(result.PoolName, input.AmountY, result.Position, input.Strategy, input.BinsBelow, input.BinsAbove, bal)

					msgText := fmt.Sprintf(
						"✅ Deployed %s\nPool: %s\nAmount: %.4f SOL\nStrategy: %s | binsBelow: %d\nPosition: %s",
						c.Name, c.PoolAddress, input.AmountY, input.Strategy, input.BinsBelow, result.Position,
					)
					telegram.SendMessageToChat(msg.Chat.ID, msgText)
				} else {
					telegram.SendMessageToChat(msg.Chat.ID, "❌ Deploy failed: "+result.Error)
				}
			}()

		case "/briefing":
			telegram.SendMessageToChat(msg.Chat.ID, "🔄 Generating morning briefing...")
			go func() {
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
					telegram.SendMessageToChat(msg.Chat.ID, "❌ Error generating briefing: "+err.Error())
					return
				}
				telegram.SendHTMLToChat(msg.Chat.ID, result.Content)
			}()

		case "/hive":
			isManualPull := text == "/hive pull"

			if cfg.HiveMind.APIKey == "" {
				telegram.SendMessageToChat(msg.Chat.ID, fmt.Sprintf("HiveMind: disabled\nAgent ID: %s\nSet hiveMind.apiKey to connect.", cfg.HiveMind.AgentID))
				return
			}

			telegram.SendMessageToChat(msg.Chat.ID, "🔄 Syncing with HiveMind...")
			go func() {
				reason := "telegram_status"
				if isManualPull {
					reason = "telegram_pull"
				}
				hivemind.RegisterHiveMindAgent(reason)

				if isManualPull {
					hivemind.PullHiveMindLessons(12)
					hivemind.PullHiveMindPresets()
				}

				pullMode := cfg.HiveMind.PullMode
				if pullMode == "" {
					pullMode = "auto"
				}

				msgText := fmt.Sprintf(
					"HiveMind: enabled\n"+
					"Agent ID: %s\n"+
					"URL: %s\n"+
					"Pull mode: %s\n"+
					"Register: ok",
					cfg.HiveMind.AgentID,
					cfg.HiveMind.URL,
					pullMode,
				)
				if isManualPull {
					msgText += "\nManual pull: completed"
				}
				telegram.SendMessageToChat(msg.Chat.ID, msgText)
			}()

		case "/pause":
			StopCronJobs()
			CronRunning = false
			telegram.SendMessageToChat(msg.Chat.ID, "⏸ Paused autonomous cycles. Telegram control still works. Use /resume to start again.")

		case "/resume":
			if !CronRunning {
				StartCronJobs(cfg)
				CronRunning = true
				telegram.SendMessageToChat(msg.Chat.ID, "▶️ Autonomous cycles resumed.")
			} else {
				telegram.SendMessageToChat(msg.Chat.ID, "Autonomous cycles are already running.")
			}

		case "/stop":
			telegram.SendMessageToChat(msg.Chat.ID, "🛑 Shutting down agent...")
			time.Sleep(1 * time.Second)
			os.Exit(0)

		case "/manage":
			telegram.SendMessageToChat(msg.Chat.ID, "🔄 Manual management cycle triggered...")
			go func() {
				if atomic.LoadUint32(&managementBusy) == 1 {
					telegram.SendMessageToChat(msg.Chat.ID, "⚠️ Management cycle is already busy")
					return
				}
				atomic.StoreUint32(&managementBusy, 1)
				ManagementLastRun = time.Now()
				defer atomic.StoreUint32(&managementBusy, 0)
				runManagementCycle(cfg, false)
			}()

		default:
			telegram.SendMessageToChat(msg.Chat.ID, "❓ Unknown command. Type /help to see all available commands.")
		}
	} else {
		// 3. User is asking a direct question - forward it to the General AI Agent loop!
		telegram.SendMessageToChat(msg.Chat.ID, "🧠 Thinking...")
		go func() {
			result, err := agent.AgentLoop(
				fmt.Sprintf("USER DIRECT QUERY:\n%s\n", text),
				cfg.LLM.MaxSteps,
				nil,
				"GENERAL",
				cfg.LLM.GeneralModel,
				2048,
				false,
				nil,
			)
			if err != nil {
				telegram.SendMessageToChat(msg.Chat.ID, "❌ Error processing request: "+err.Error())
				return
			}
			telegram.SendMessageToChat(msg.Chat.ID, result.Content)
		}()
	}
}

func SetPositionInstruction(addr string, instruction string) error {
	if registry.State == nil {
		return fmt.Errorf("state store not initialized")
	}
	stateMap := registry.State.GetPositions()
	pos, exists := stateMap[addr]
	if !exists {
		pos = types.PositionState{
			Position: addr,
		}
	}
	pos.Instruction = instruction
	return registry.State.UpdatePosition(addr, pos)
}

func updateConfigValue(cfg *config.Config, key string, valStr string) error {
	keyLower := strings.ToLower(key)
	switch keyLower {
	case "strategy":
		cfg.Strategy.Strategy = valStr
	case "minbinsbelow":
		var v int
		if _, err := fmt.Sscanf(valStr, "%d", &v); err != nil {
			return err
		}
		cfg.Strategy.MinBinsBelow = v
	case "maxbinsbelow":
		var v int
		if _, err := fmt.Sscanf(valStr, "%d", &v); err != nil {
			return err
		}
		cfg.Strategy.MaxBinsBelow = v
	case "defaultbinsbelow":
		var v int
		if _, err := fmt.Sscanf(valStr, "%d", &v); err != nil {
			return err
		}
		cfg.Strategy.DefaultBinsBelow = v
	case "deployamountsol":
		var v float64
		if _, err := fmt.Sscanf(valStr, "%f", &v); err != nil {
			return err
		}
		cfg.Management.DeployAmountSol = v
	case "maxdeployamount":
		var v float64
		if _, err := fmt.Sscanf(valStr, "%f", &v); err != nil {
			return err
		}
		cfg.Risk.MaxDeployAmount = v
	case "mintvl":
		var v float64
		if _, err := fmt.Sscanf(valStr, "%f", &v); err != nil {
			return err
		}
		cfg.Screening.MinTvl = v
	case "maxtvl":
		var v float64
		if _, err := fmt.Sscanf(valStr, "%f", &v); err != nil {
			return err
		}
		cfg.Screening.MaxTvl = v
	case "minsoltoopen":
		var v float64
		if _, err := fmt.Sscanf(valStr, "%f", &v); err != nil {
			return err
		}
		cfg.Management.MinSolToOpen = v
	case "gasreserve":
		var v float64
		if _, err := fmt.Sscanf(valStr, "%f", &v); err != nil {
			return err
		}
		cfg.Management.GasReserve = v
	case "positionsizepct":
		var v float64
		if _, err := fmt.Sscanf(valStr, "%f", &v); err != nil {
			return err
		}
		cfg.Management.PositionSizePct = v
	case "maxpositions":
		var v int
		if _, err := fmt.Sscanf(valStr, "%d", &v); err != nil {
			return err
		}
		cfg.Risk.MaxPositions = v
	case "stoplosspct":
		var v float64
		if _, err := fmt.Sscanf(valStr, "%f", &v); err != nil {
			return err
		}
		cfg.Management.StopLossPct = v
	case "takeprofitpct":
		var v float64
		if _, err := fmt.Sscanf(valStr, "%f", &v); err != nil {
			return err
		}
		cfg.Management.TakeProfitPct = v
	case "managementintervalmin":
		var v int
		if _, err := fmt.Sscanf(valStr, "%d", &v); err != nil {
			return err
		}
		cfg.Schedule.ManagementIntervalMin = v
	case "screeningintervalmin":
		var v int
		if _, err := fmt.Sscanf(valStr, "%d", &v); err != nil {
			return err
		}
		cfg.Schedule.ScreeningIntervalMin = v
	case "dryrun":
		v := strings.ToLower(valStr) == "true"
		cfg.DryRun = v
	default:
		return fmt.Errorf("unknown or unsupported config key: %s", key)
	}
	return config.SaveConfig(cfg)
}

func formatWalletStatus(cfg *config.Config) (string, error) {
	walletAddr := cfg.WalletAddress()
	if walletAddr == "" {
		return "", fmt.Errorf("wallet address not configured")
	}
	balances, err := solana.GetHeliusBalances(walletAddr, cfg.Tokens.SOL, cfg.Tokens.USDC)
	if err != nil {
		return "", err
	}
	client := dlmm.NewClient(walletAddr, cfg.RPCURLOrDefault())
	positions, err := client.GetMyPositions(true)
	if err != nil {
		return "", err
	}
	deployAmount := config.ComputeDeployAmount(balances.SOL, cfg)
	minOpenBal := config.ComputeMinOpenBalance(cfg)

	dryStr := "no"
	if cfg.DryRun {
		dryStr = "yes"
	}

	hiveStr := "off"
	if cfg.HiveMind.APIKey != "" {
		hiveStr = "on"
	}

	totalPos := len(positions.Positions)

	status := fmt.Sprintf(
		"Wallet: %.4f SOL ($%.2f)\n"+
			"SOL price: $%.2f\n"+
			"Open positions: %d/%d\n"+
			"Next deploy amount: %.4f SOL\n"+
			"Min balance to open: %.2f SOL\n"+
			"Dry run: %s\n"+
			"HiveMind: %s",
		balances.SOL, balances.SOL*balances.SOLPrice,
		balances.SOLPrice,
		totalPos, cfg.Risk.MaxPositions,
		deployAmount,
		minOpenBal,
		dryStr,
		hiveStr,
	)
	return status, nil
}

func formatConfigSnapshot(cfg *config.Config) string {
	hiveStr := "disabled"
	if cfg.HiveMind.APIKey != "" {
		hiveStr = "enabled"
	}
	trailingStr := "off"
	if cfg.Management.TrailingTakeProfit {
		trailingStr = "on"
	}
	repeatDeployStr := "off"
	if cfg.Management.RepeatDeployCooldownEnabled {
		repeatDeployStr = "on"
	}

	minOpenBal := config.ComputeMinOpenBalance(cfg)

	return fmt.Sprintf(
		"Config snapshot\n\n"+
			"Strategy: %s | binsBelow: %d-%d | default %d\n"+
			"Deploy: %.4f SOL | gasReserve: %.4f | min open %.2f SOL | maxPositions: %d\n"+
			"Stop loss: %.1f%% | take profit: %.1f%%\n"+
			"Trailing: %s | trigger %.1f%% | drop %.1f%%\n"+
			"OOR: %dm | cooldown %dx / %dh\n"+
			"Repeat deploy cooldown: %s | %dx / %dh | min fee earned %.1f%% | %s\n"+
			"Yield floor: %.1f%% | min age %dm\n"+
			"Screening: %s / %s | TVL %.0f-%.0f\n"+
			"Intervals: manage %dm | screen %dm\n"+
			"HiveMind: %s | %s",
		cfg.Strategy.Strategy, cfg.Strategy.MinBinsBelow, cfg.Strategy.MaxBinsBelow, cfg.Strategy.DefaultBinsBelow,
		cfg.Management.DeployAmountSol, cfg.Management.GasReserve, minOpenBal, cfg.Risk.MaxPositions,
		cfg.Management.StopLossPct, cfg.Management.TakeProfitPct,
		trailingStr, cfg.Management.TrailingTriggerPct, cfg.Management.TrailingDropPct,
		cfg.Management.OutOfRangeWaitMinutes, cfg.Management.OorCooldownTriggerCount, cfg.Management.OorCooldownHours,
		repeatDeployStr, cfg.Management.RepeatDeployCooldownTriggerCount, cfg.Management.RepeatDeployCooldownHours, cfg.Management.RepeatDeployCooldownMinFeeEarnedPct, cfg.Management.RepeatDeployCooldownScope,
		cfg.Management.MinFeePerTvl24h, cfg.Management.MinAgeBeforeYieldCheck,
		cfg.Screening.Category, cfg.Screening.Timeframe, cfg.Screening.MinTvl, cfg.Screening.MaxTvl,
		cfg.Schedule.ManagementIntervalMin, cfg.Schedule.ScreeningIntervalMin,
		hiveStr, cfg.HiveMind.AgentID,
	)
}
