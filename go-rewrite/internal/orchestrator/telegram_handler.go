package orchestrator

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"meridian-go-rewrite/internal/agent"
	"meridian-go-rewrite/internal/config"
	"meridian-go-rewrite/internal/logger"
	"meridian-go-rewrite/internal/solana"
	"meridian-go-rewrite/internal/solana/dlmm"
	"meridian-go-rewrite/internal/telegram"
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
			helpMsg := "🤖 <b>Meridian Autonomous LP Agent</b> 🤖\n\n" +
				"Available Commands:\n" +
				"• <b>/status</b> - Show bot configuration and current cycle status.\n" +
				"• <b>/balance</b> - Query live Solana wallet balance.\n" +
				"• <b>/positions</b> - List active DLMM liquidity positions.\n" +
				"• <b>/screen</b> - Trigger manual screening cycle.\n" +
				"• <b>/manage</b> - Trigger manual management cycle.\n" +
				"• <b>/help</b> - Show this message.\n\n" +
				"Or ask me anything directly! I can explain strategies, analyze markets, or look up pool history."
			telegram.SendHTMLToChat(msg.Chat.ID, helpMsg)

		case "/status":
			mode := "LIVE"
			if cfg.DryRun {
				mode = "DRY RUN"
			}
			walletAddr := cfg.WalletAddress()
			var walletShort string
			if walletAddr != "" {
				walletShort = walletAddr[:6] + "..." + walletAddr[len(walletAddr)-4:]
			} else {
				walletShort = "Not configured"
			}

			statusMsg := fmt.Sprintf("ℹ️ <b>Meridian Status</b>\n\n"+
				"• <b>Mode</b>: %s\n"+
				"• <b>Wallet</b>: <code>%s</code>\n"+
				"• <b>Management Cycle</b>: %dm (Next in: %s)\n"+
				"• <b>Screening Cycle</b>: %dm (Next in: %s)\n"+
				"• <b>Strategy</b>: %s\n"+
				"• <b>Min Open Balance</b>: %.4f SOL\n"+
				"• <b>Max Positions</b>: %d",
				mode,
				walletShort,
				cfg.Schedule.ManagementIntervalMin,
				NextRunIn(ManagementLastRun, cfg.Schedule.ManagementIntervalMin),
				cfg.Schedule.ScreeningIntervalMin,
				NextRunIn(ScreeningLastRun, cfg.Schedule.ScreeningIntervalMin),
				cfg.Strategy.Strategy,
				config.ComputeMinOpenBalance(cfg),
				cfg.Risk.MaxPositions,
			)
			telegram.SendHTMLToChat(msg.Chat.ID, statusMsg)

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
			sb.WriteString("💼 <b>Active DLMM Positions</b>\n\n")
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

		case "/screen":
			telegram.SendMessageToChat(msg.Chat.ID, "🔄 Manual screening cycle triggered...")
			go func() {
				if atomic.LoadUint32(&screeningBusy) == 1 {
					telegram.SendMessageToChat(msg.Chat.ID, "⚠️ Screening cycle is already busy")
					return
				}
				atomic.StoreUint32(&screeningBusy, 1)
				ScreeningLastRun = time.Now()
				defer atomic.StoreUint32(&screeningBusy, 0)
				runScreeningCycle(cfg, false)
			}()

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
