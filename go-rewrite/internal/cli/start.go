package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"meridian-go-rewrite/internal/agent"
	"meridian-go-rewrite/internal/config"
	"meridian-go-rewrite/internal/discord"
	"meridian-go-rewrite/internal/orchestrator"
	"meridian-go-rewrite/internal/registry"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the autonomous agent",
	Long:  "Start the Meridian DLMM LP agent with cron-driven management and screening cycles.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Get()
		if cfg == nil {
			return fmt.Errorf("config not loaded")
		}

		walletAddr := cfg.WalletAddress()
		rpcURL := cfg.RPCURLOrDefault()
		agent.SetToolDeps(walletAddr, rpcURL)

		dataDir := cfg.DataDir
		if dataDir == "" {
			dataDir = os.Getenv("HOME") + "/.meridian"
		}
		os.MkdirAll(dataDir, 0755)
		if err := registry.Init(dataDir); err != nil {
			fmt.Printf("WARNING: persistence init failed: %v\n", err)
		}

		fmt.Println("╔═══════════════════════════════════════════╗")
		fmt.Println("║      DLMM LP Agent — Go Edition           ║")
		fmt.Println("╚═══════════════════════════════════════════╝")
		fmt.Println()

		mode := "LIVE"
		if cfg.DryRun {
			mode = "DRY RUN"
		}
		fmt.Printf("Mode: %s (opens at >= %.4f SOL)\n", mode, config.ComputeMinOpenBalance(cfg))
		if walletAddr != "" {
			fmt.Printf("Wallet: %s...%s\n", walletAddr[:6], walletAddr[len(walletAddr)-4:])
		}

		orchestrator.StartCronJobs(cfg)
		orchestrator.StartTelegramBot(cfg)

		if cfg.Screening.UseDiscordSignals {
			token := os.Getenv("DISCORD_USER_TOKEN")
			guildID := os.Getenv("DISCORD_GUILD_ID")
			channelIDsRaw := os.Getenv("DISCORD_CHANNEL_IDS")
			if token != "" && guildID != "" && channelIDsRaw != "" {
				channels := strings.Split(channelIDsRaw, ",")
				var parsedChannels []string
				for _, ch := range channels {
					ch = strings.TrimSpace(ch)
					if ch != "" {
						parsedChannels = append(parsedChannels, ch)
					}
				}
				go func() {
					if err := discord.StartListener(token, guildID, parsedChannels); err != nil {
						fmt.Printf("Discord listener error: %v\n", err)
					}
				}()
			} else {
				fmt.Println("Discord listener enabled but missing config (DISCORD_USER_TOKEN, DISCORD_GUILD_ID, DISCORD_CHANNEL_IDS)")
			}
		}

		select {}
	},
}

func init() {
	RootCmd.AddCommand(startCmd)
}
