package telegram

import (
	"fmt"
	"html"
	"log"
	"regexp"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"meridian-go-rewrite/internal/config"
)

var (
	bot             *tgbotapi.BotAPI
	chatID          int64
	messageHandlers []func(*tgbotapi.Message)
)

func StartPolling(cfg *config.Config, handler func(*tgbotapi.Message)) error {
	if cfg.Telegram.BotToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN not set")
	}

	var err error
	bot, err = tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		return err
	}

	if cfg.Telegram.ChatID != "" {
		chatID = parseInt64(cfg.Telegram.ChatID)
	}

	messageHandlers = []func(*tgbotapi.Message){handler}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := bot.GetUpdatesChan(u)

	go func() {
		for update := range updates {
			if update.Message != nil {
				for _, h := range messageHandlers {
					h(update.Message)
				}
			}
			if update.CallbackQuery != nil {
				handleCallback(update.CallbackQuery)
			}
		}
	}()

	log.Printf("[telegram] Bot polling started")
	return nil
}

func SendMessage(text string) error {
	if bot == nil || chatID == 0 {
		return fmt.Errorf("telegram not configured")
	}
	text = truncate(text, 4096)
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := bot.Send(msg)
	return err
}

func SendMessageToChat(targetChatID int64, text string) error {
	if bot == nil {
		return fmt.Errorf("telegram bot not initialized")
	}
	text = truncate(text, 4096)
	msg := tgbotapi.NewMessage(targetChatID, text)
	_, err := bot.Send(msg)
	return err
}

func SendHTML(html string) error {
	if bot == nil || chatID == 0 {
		return fmt.Errorf("telegram not configured")
	}
	html = truncate(html, 4096)
	msg := tgbotapi.NewMessage(chatID, html)
	msg.ParseMode = "HTML"
	_, err := bot.Send(msg)
	return err
}

func SendHTMLToChat(targetChatID int64, html string) error {
	if bot == nil {
		return fmt.Errorf("telegram bot not initialized")
	}
	html = truncate(html, 4096)
	msg := tgbotapi.NewMessage(targetChatID, html)
	msg.ParseMode = "HTML"
	_, err := bot.Send(msg)
	return err
}

func EditMessage(text string, messageID int) error {
	if bot == nil || chatID == 0 {
		return nil
	}
	text = truncate(text, 4096)
	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	_, err := bot.Send(msg)
	return err
}

func SendWithButtons(text string, keyboard [][]tgbotapi.InlineKeyboardButton) error {
	if bot == nil || chatID == 0 {
		return fmt.Errorf("telegram not configured")
	}
	text = truncate(text, 4096)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(keyboard...)
	_, err := bot.Send(msg)
	return err
}

func EditWithButtons(text string, messageID int, keyboard [][]tgbotapi.InlineKeyboardButton) error {
	if bot == nil || chatID == 0 {
		return nil
	}
	text = truncate(text, 4096)
	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	msg.ReplyMarkup = &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: keyboard}
	_, err := bot.Send(msg)
	return err
}

func handleCallback(callback *tgbotapi.CallbackQuery) {
	data := callback.Data
	if strings.HasPrefix(data, "cfg:") {
		answerCallback(callback.ID, "Config updated")
		EditMessage("Settings updated.", callback.Message.MessageID)
		return
	}
	answerCallback(callback.ID, "")
}

func answerCallback(id, text string) {
	if bot == nil {
		return
	}
	callback := tgbotapi.NewCallback(id, text)
	bot.Request(callback)
}

type LiveMessage struct {
	bot       *tgbotapi.BotAPI
	chatID    int64
	msgID     int
	title     string
	toolLines []string
	footer    string
}

var (
	boldRegex   = regexp.MustCompile(`\*\*(.*?)\*\*`)
	italicRegex = regexp.MustCompile(`\*(.*?)\*`)
	codeRegex   = regexp.MustCompile("`(.*?)`")
)

func parseMarkdownToHTML(text string) string {
	escaped := html.EscapeString(text)
	escaped = boldRegex.ReplaceAllString(escaped, "<b>$1</b>")
	escaped = italicRegex.ReplaceAllString(escaped, "<i>$1</i>")
	escaped = codeRegex.ReplaceAllString(escaped, "<code>$1</code>")
	return escaped
}

func CreateLiveMessage(title, intro string) (*LiveMessage, error) {
	if bot == nil || chatID == 0 {
		return nil, nil
	}
	text := title + "\n\n" + html.EscapeString(intro)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	sent, err := bot.Send(msg)
	if err != nil {
		return nil, err
	}
	return &LiveMessage{
		bot:    bot,
		chatID: chatID,
		msgID:  sent.MessageID,
		title:  title,
	}, nil
}

func (lm *LiveMessage) ToolStart(name string) {
	label := name
	switch name {
	case "deploy_position":
		label = "deploy position"
	case "close_position":
		label = "close position"
	case "claim_fees":
		label = "claim fees"
	case "get_top_candidates":
		label = "get top candidates"
	case "get_my_positions":
		label = "get positions"
	case "get_wallet_balance":
		label = "get wallet balance"
	case "get_active_bin":
		label = "get active bin"
	case "get_token_info":
		label = "get token info"
	}
	lm.toolLines = append(lm.toolLines, "ℹ️ "+label+" ...")
	lm.flush()
}

func (lm *LiveMessage) ToolFinish(name string, result any, success bool) {
	label := name
	switch name {
	case "deploy_position":
		label = "deploy position"
	case "close_position":
		label = "close position"
	case "claim_fees":
		label = "claim fees"
	default:
		label = name
	}
	icon := "✅"
	if !success {
		icon = "❌"
	}
	lm.toolLines = append(lm.toolLines, icon+" "+label)
	lm.flush()
}

func (lm *LiveMessage) Finalize(text string) error {
	lm.footer = parseMarkdownToHTML(text)
	return lm.flush()
}

func (lm *LiveMessage) Fail(text string) error {
	lm.footer = "❌ " + html.EscapeString(text)
	return lm.flush()
}

func (lm *LiveMessage) flush() error {
	if lm.bot == nil || lm.msgID == 0 {
		return nil
	}
	parts := []string{lm.title}
	if len(lm.toolLines) > 0 {
		var escapedTools []string
		for _, line := range lm.toolLines {
			escapedTools = append(escapedTools, html.EscapeString(line))
		}
		parts = append(parts, strings.Join(escapedTools, "\n"))
	}
	if lm.footer != "" {
		parts = append(parts, lm.footer)
	}
	text := strings.Join(parts, "\n\n")
	text = truncate(text, 4096)
	msg := tgbotapi.NewEditMessageText(lm.chatID, lm.msgID, text)
	msg.ParseMode = "HTML"
	_, err := lm.bot.Send(msg)
	return err
}

func (lm *LiveMessage) Note(text string) {
	lm.toolLines = append(lm.toolLines, parseMarkdownToHTML(text))
	lm.flush()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func parseInt64(s string) int64 {
	var result int64
	fmt.Sscanf(s, "%d", &result)
	return result
}

func NotifyDeploy(pair string, amountSol float64, position, strategy string, binsBelow, binsAbove int, balanceSol float64) {
	if bot == nil || chatID == 0 {
		return
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🚀 <b>New Position Deployed: %s</b>\n\n", pair))
	sb.WriteString(fmt.Sprintf("• <b>Size:</b> <code>%.2f SOL</code>\n", amountSol))
	sb.WriteString(fmt.Sprintf("• <b>Strategy:</b> <code>%s</code> (Bins: -%d to +%d)\n", strategy, binsBelow, binsAbove))
	sb.WriteString(fmt.Sprintf("• <b>Remaining Wallet:</b> <code>%.4f SOL</code>\n\n", balanceSol))
	
	if position != "" {
		sb.WriteString(fmt.Sprintf("🔗 <a href=\"https://solscan.io/account/%s\">View Position on Solscan</a>", position))
	}
	
	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "HTML"
	msg.DisableWebPagePreview = true
	bot.Send(msg)
}

func NotifyClose(pair string, pnlUSD, pnlPct float64, reason string, feesUSD float64, balanceSol float64) {
	if bot == nil || chatID == 0 {
		return
	}
	sign := ""
	if pnlUSD >= 0 {
		sign = "+"
	}
	
	icon := "🔒"
	if reason == "stop_loss" {
		icon = "🚨"
	} else if reason == "take_profit" {
		icon = "🎯"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s <b>Position Closed: %s</b>\n\n", icon, pair))
	sb.WriteString(fmt.Sprintf("• <b>Exit PnL:</b> <code>%s$%.2f (%s%.2f%%)</code>\n", sign, pnlUSD, sign, pnlPct))
	if feesUSD > 0 {
		sb.WriteString(fmt.Sprintf("• <b>Fees Collected:</b> <code>$%.2f</code>\n", feesUSD))
	}
	if reason != "" {
		sb.WriteString(fmt.Sprintf("• <b>Exit Reason:</b> <code>%s</code>\n", strings.ReplaceAll(reason, "_", " ")))
	}
	sb.WriteString(fmt.Sprintf("• <b>Remaining Wallet:</b> <code>%.4f SOL</code>\n", balanceSol))

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "HTML"
	bot.Send(msg)
}

func NotifyOOR(pair string, minutesOOR int) {
	if bot == nil || chatID == 0 {
		return
	}
	text := fmt.Sprintf("⚠️ <b>Out of Range</b> %s\n%d minutes OOR", pair, minutesOOR)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	bot.Send(msg)
}

func truncateMID(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func IsEnabled() bool {
	return bot != nil && chatID != 0
}
