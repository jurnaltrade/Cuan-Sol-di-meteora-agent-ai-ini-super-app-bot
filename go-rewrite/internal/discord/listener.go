package discord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"meridian-go-rewrite/internal/logger"
)

var solAddrRe = regexp.MustCompile(`[1-9A-HJ-NP-Za-km-z]{32,44}`)
var falsePositiveSkip = map[string]bool{
	"solana": true, "meteora": true, "jupiter": true, "raydium": true, "orca": true,
}

func isLikelySolanaAddress(str string) bool {
	if len(str) < 32 || len(str) > 44 {
		return false
	}
	if falsePositiveSkip[strings.ToLower(str)] {
		return false
	}
	hasDigit := false
	for _, c := range str {
		if c >= '0' && c <= '9' {
			hasDigit = true
			break
		}
	}
	return hasDigit
}

type DiscordSignal struct {
	ID                    string   `json:"id"`
	PoolAddress           string   `json:"pool_address"`
	BaseMint              string   `json:"base_mint"`
	BaseSymbol            string   `json:"base_symbol"`
	SignalSource          string   `json:"signal_source"`
	DiscordGuild          string   `json:"discord_guild"`
	DiscordChannel        string   `json:"discord_channel"`
	DiscordAuthor         string   `json:"discord_author"`
	DiscordMessageSnippet string   `json:"discord_message_snippet"`
	QueuedAt              string   `json:"queued_at"`
	RugScore              *float64 `json:"rug_score"`
	TotalFeesSol          *float64 `json:"total_fees_sol"`
	TokenAgeMinutes       *int     `json:"token_age_minutes"`
	Status                string   `json:"status"`
}

var signalsMutex sync.Mutex

func loadSignals() []DiscordSignal {
	signalsMutex.Lock()
	defer signalsMutex.Unlock()
	data, err := os.ReadFile("discord-signals.json")
	if err != nil {
		return []DiscordSignal{}
	}
	var sigs []DiscordSignal
	json.Unmarshal(data, &sigs)
	return sigs
}

func saveSignal(record DiscordSignal) {
	signalsMutex.Lock()
	defer signalsMutex.Unlock()
	data, err := os.ReadFile("discord-signals.json")
	var sigs []DiscordSignal
	if err == nil {
		json.Unmarshal(data, &sigs)
	}

	sigs = append([]DiscordSignal{record}, sigs...)
	if len(sigs) > 100 {
		sigs = sigs[:100]
	}

	if dir := filepath.Dir("discord-signals.json"); dir != "." {
		os.MkdirAll(dir, 0755)
	}
	bytes, _ := json.MarshalIndent(sigs, "", "  ")
	os.WriteFile("discord-signals.json", bytes, 0644)
}

func processAddress(address string, m *discordgo.MessageCreate) {
	res := RunPreChecks(address)
	if !res.Pass {
		return
	}

	snippet := m.Content
	if len(snippet) > 120 {
		snippet = snippet[:120]
	}

	guildName := "unknown"
	// Can fetch guild name using m.GuildID if needed

	channelName := "unknown"
	// Can fetch channel name using m.ChannelID if needed

	record := DiscordSignal{
		ID:                    fmt.Sprintf("%s-%d", address[:8], time.Now().UnixMilli()),
		PoolAddress:           res.PoolAddress,
		BaseMint:              res.BaseMint,
		BaseSymbol:            res.Symbol,
		SignalSource:          "discord",
		DiscordGuild:          guildName,
		DiscordChannel:        channelName,
		DiscordAuthor:         m.Author.Username,
		DiscordMessageSnippet: snippet,
		QueuedAt:              time.Now().UTC().Format(time.RFC3339),
		RugScore:              res.RugScore,
		TotalFeesSol:          res.TotalFeesSol,
		TokenAgeMinutes:       res.TokenAgeMinutes,
		Status:                "pending",
	}

	saveSignal(record)
	fmt.Printf("\n[QUEUED] %s → %s\n", record.BaseSymbol, record.PoolAddress)
	fmt.Printf("  from: @%s\n", record.DiscordAuthor)
}

func StartListener(token, guildID string, channelIDs []string) error {
	if token == "" || guildID == "" || len(channelIDs) == 0 {
		return fmt.Errorf("missing discord configuration")
	}

	dg, err := discordgo.New(token)
	if err != nil {
		return err
	}

	allowedChannels := make(map[string]bool)
	for _, id := range channelIDs {
		allowedChannels[id] = true
	}

	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.GuildID != guildID {
			return
		}
		if !allowedChannels[m.ChannelID] {
			return
		}
		if m.Author.ID == s.State.User.ID {
			return
		}
		if m.Author.Username != "Metlex Pool Bot" {
			return
		}

		content := m.Content
		for _, e := range m.Embeds {
			content += " " + e.Title + " " + e.Description
		}

		matches := solAddrRe.FindAllString(content, -1)
		uniqueMap := make(map[string]bool)
		for _, m := range matches {
			if isLikelySolanaAddress(m) {
				uniqueMap[m] = true
			}
		}

		if len(uniqueMap) == 0 {
			return
		}

		logger.Log("discord", fmt.Sprintf("Addresses found: %v", uniqueMap))
		for addr := range uniqueMap {
			go processAddress(addr, m)
		}
	})

	err = dg.Open()
	if err != nil {
		return err
	}

	logger.Log("discord", "Discord listener started")
	return nil
}
