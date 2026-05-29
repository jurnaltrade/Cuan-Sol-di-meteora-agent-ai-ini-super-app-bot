package config

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/mr-tron/base58"
)

var (
	activeConfig        atomic.Value
	ConfigDir           string
	ConfigFile          string
	MIN_SAFE_BINS_BELOW = 35
)

type WalletConfig struct {
	PrivateKey  string `json:"privateKey"`
	KeypairPath string `json:"keypairPath"`
	Password    string `json:"password"`
}

type RiskConfig struct {
	MaxPositions    int     `json:"maxPositions"`
	MaxDeployAmount float64 `json:"maxDeployAmount"`
}

type ScreeningConfig struct {
	PageSize                       int      `json:"pageSize"`
	FilterBy                       string   `json:"filterBy"`
	ExcludeHighSupplyConcentration bool     `json:"excludeHighSupplyConcentration"`
	MinActivePositions             int      `json:"minActivePositions"`
	MinFeeActiveTvlRatio           float64  `json:"minFeeActiveTvlRatio"`
	MinTvl                         float64  `json:"minTvl"`
	MaxTvl                         float64  `json:"maxTvl"`
	MinVolume                      float64  `json:"minVolume"`
	MinOrganic                     float64  `json:"minOrganic"`
	MinQuoteOrganic                float64  `json:"minQuoteOrganic"`
	MinHolders                     float64  `json:"minHolders"`
	MinMcap                        float64  `json:"minMcap"`
	MaxMcap                        float64  `json:"maxMcap"`
	MinBinStep                     float64  `json:"minBinStep"`
	MaxBinStep                     float64  `json:"maxBinStep"`
	Timeframe                      string   `json:"timeframe"`
	Category                       string   `json:"category"`
	MinTokenFeesSol                float64  `json:"minTokenFeesSol"`
	UseDiscordSignals              bool     `json:"useDiscordSignals"`
	DiscordSignalMode              string   `json:"discordSignalMode"`
	AvoidPvpSymbols                bool     `json:"avoidPvpSymbols"`
	BlockPvpSymbols                bool     `json:"blockPvpSymbols"`
	MaxBundlePct                   float64  `json:"maxBundlePct"`
	MaxBotHoldersPct               float64  `json:"maxBotHoldersPct"`
	MaxTop10Pct                    float64  `json:"maxTop10Pct"`
	AllowedLaunchpads              []string `json:"allowedLaunchpads"`
	BlockedLaunchpads              []string `json:"blockedLaunchpads"`
	MinTokenAgeHours               *float64 `json:"minTokenAgeHours"`
	MaxTokenAgeHours               *float64 `json:"maxTokenAgeHours"`
	AthFilterPct                   *float64 `json:"athFilterPct"`
	MaxVolatility                  *float64 `json:"maxVolatility"`
}

type ManagementConfig struct {
	MinClaimAmount                      float64 `json:"minClaimAmount"`
	AutoSwapAfterClaim                  bool    `json:"autoSwapAfterClaim"`
	OutOfRangeBinsToClose               int     `json:"outOfRangeBinsToClose"`
	OutOfRangeWaitMinutes               int     `json:"outOfRangeWaitMinutes"`
	OorCooldownTriggerCount             int     `json:"oorCooldownTriggerCount"`
	OorCooldownHours                    int     `json:"oorCooldownHours"`
	RepeatDeployCooldownEnabled         bool    `json:"repeatDeployCooldownEnabled"`
	RepeatDeployCooldownTriggerCount    int     `json:"repeatDeployCooldownTriggerCount"`
	RepeatDeployCooldownHours           int     `json:"repeatDeployCooldownHours"`
	RepeatDeployCooldownScope           string  `json:"repeatDeployCooldownScope"`
	RepeatDeployCooldownMinFeeEarnedPct float64 `json:"repeatDeployCooldownMinFeeEarnedPct"`
	MinVolumeToRebalance                float64 `json:"minVolumeToRebalance"`
	StopLossPct                         float64 `json:"stopLossPct"`
	TakeProfitPct                       float64 `json:"takeProfitPct"`
	MinFeePerTvl24h                     float64 `json:"minFeePerTvl24h"`
	MinAgeBeforeYieldCheck              int     `json:"minAgeBeforeYieldCheck"`
	MinSolToOpen                        float64 `json:"minSolToOpen"`
	DeployAmountSol                     float64 `json:"deployAmountSol"`
	GasReserve                          float64 `json:"gasReserve"`
	PositionSizePct                     float64 `json:"positionSizePct"`
	TrailingTakeProfit                  bool    `json:"trailingTakeProfit"`
	TrailingTriggerPct                  float64 `json:"trailingTriggerPct"`
	TrailingDropPct                     float64 `json:"trailingDropPct"`
	PnlSanityMaxDiffPct                 float64 `json:"pnlSanityMaxDiffPct"`
	SolMode                             bool    `json:"solMode"`
}

type StrategyConfig struct {
	Strategy         string `json:"strategy"`
	MinBinsBelow     int    `json:"minBinsBelow"`
	MaxBinsBelow     int    `json:"maxBinsBelow"`
	DefaultBinsBelow int    `json:"defaultBinsBelow"`
}

type ScheduleConfig struct {
	ManagementIntervalMin  int `json:"managementIntervalMin"`
	ScreeningIntervalMin   int `json:"screeningIntervalMin"`
	HealthCheckIntervalMin int `json:"healthCheckIntervalMin"`
	PnlPollIntervalSec     int `json:"pnlPollIntervalSec"`
}

type LLMConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxTokens       int     `json:"maxTokens"`
	MaxSteps        int     `json:"maxSteps"`
	ManagementModel string  `json:"managementModel"`
	ScreeningModel  string  `json:"screeningModel"`
	GeneralModel    string  `json:"generalModel"`
}

type TelegramConfig struct {
	BotToken     string   `json:"botToken"`
	ChatID       string   `json:"chatId"`
	AllowedUsers []string `json:"allowedUsers"`
}

type HiveMindConfig struct {
	URL      string `json:"url"`
	APIKey   string `json:"apiKey"`
	AgentID  string `json:"agentId"`
	PullMode string `json:"pullMode"`
}

type APIConfig struct {
	URL                 string `json:"url"`
	PublicAPIKey        string `json:"publicApiKey"`
	LPAgentRelayEnabled bool   `json:"lpAgentRelayEnabled"`
}

type JupiterConfig struct {
	APIKey          string `json:"apiKey"`
	ReferralAccount string `json:"referralAccount"`
	ReferralFeeBps  int    `json:"referralFeeBps"`
}

type DarwinConfig struct {
	Enabled       bool    `json:"enabled"`
	WindowDays    int     `json:"windowDays"`
	RecalcEvery   int     `json:"recalcEvery"`
	BoostFactor   float64 `json:"boostFactor"`
	DecayFactor   float64 `json:"decayFactor"`
	WeightFloor   float64 `json:"weightFloor"`
	WeightCeiling float64 `json:"weightCeiling"`
	MinSamples    int     `json:"minSamples"`
}

type TokensConfig struct {
	SOL  string `json:"SOL"`
	USDC string `json:"USDC"`
	USDT string `json:"USDT"`
}

type IndicatorsConfig struct {
	Enabled             bool     `json:"enabled"`
	EntryPreset         string   `json:"entryPreset"`
	ExitPreset          string   `json:"exitPreset"`
	RSILength           int      `json:"rsiLength"`
	Intervals           []string `json:"intervals"`
	Candles             int      `json:"candles"`
	RSIOversold         int      `json:"rsiOversold"`
	RSIOverbought       int      `json:"rsiOverbought"`
	RequireAllIntervals bool     `json:"requireAllIntervals"`
}

type Config struct {
	DataDir    string           `json:"dataDir"`
	RPCURL     string           `json:"rpcUrl"`
	WalletAddr string           `json:"walletAddr"`
	Wallet     WalletConfig     `json:"wallet"`
	Risk       RiskConfig       `json:"risk"`
	Screening  ScreeningConfig  `json:"screening"`
	Management ManagementConfig `json:"management"`
	Strategy   StrategyConfig   `json:"strategy"`
	Schedule   ScheduleConfig   `json:"schedule"`
	LLM        LLMConfig        `json:"llm"`
	Telegram   TelegramConfig   `json:"telegram"`
	HiveMind   HiveMindConfig   `json:"hiveMind"`
	API        APIConfig        `json:"api"`
	Jupiter    JupiterConfig    `json:"jupiter"`
	Darwin     DarwinConfig     `json:"darwin"`
	Tokens     TokensConfig     `json:"tokens"`
	Indicators IndicatorsConfig `json:"indicators"`
	DryRun     bool             `json:"dryRun"`
}

func DefaultConfig() *Config {
	return &Config{
		RPCURL: "https://mainnet.helius-rpc.com/?api-key=YOUR_KEY",
		Risk: RiskConfig{
			MaxPositions:    3,
			MaxDeployAmount: 50,
		},
		Screening: ScreeningConfig{
			PageSize:                       100,
			FilterBy:                       "all",
			MinActivePositions:             10,
			ExcludeHighSupplyConcentration: true,
			MinFeeActiveTvlRatio:           0.05,
			MinTvl:                         10000,
			MaxTvl:                         150000,
			MinVolume:                      500,
			MinOrganic:                     60,
			MinQuoteOrganic:                60,
			MinHolders:                     500,
			MinMcap:                        150000,
			MaxMcap:                        10000000,
			MinBinStep:                     80,
			MaxBinStep:                     125,
			Timeframe:                      "5m",
			Category:                       "trending",
			MinTokenFeesSol:                30,
			AvoidPvpSymbols:                true,
			MaxBundlePct:                   30,
			MaxBotHoldersPct:               30,
			MaxTop10Pct:                    60,
		},
		Management: ManagementConfig{
			MinClaimAmount:                   5,
			OutOfRangeBinsToClose:            10,
			OutOfRangeWaitMinutes:            30,
			OorCooldownTriggerCount:          3,
			OorCooldownHours:                 12,
			RepeatDeployCooldownEnabled:      true,
			RepeatDeployCooldownTriggerCount: 3,
			RepeatDeployCooldownHours:        12,
			RepeatDeployCooldownScope:        "token",
			MinVolumeToRebalance:             1000,
			StopLossPct:                      -50,
			TakeProfitPct:                    5,
			MinFeePerTvl24h:                  7,
			MinAgeBeforeYieldCheck:           60,
			MinSolToOpen:                     0.5,
			DeployAmountSol:                  0.4,
			GasReserve:                       0.08,
			PositionSizePct:                  0.35,
			TrailingTakeProfit:               true,
			TrailingTriggerPct:               3,
			TrailingDropPct:                  1.5,
			PnlSanityMaxDiffPct:              5,
		},
		Strategy: StrategyConfig{
			Strategy:         "bid_ask",
			MinBinsBelow:     MIN_SAFE_BINS_BELOW,
			MaxBinsBelow:     69,
			DefaultBinsBelow: 69,
		},
		Schedule: ScheduleConfig{
			ManagementIntervalMin:  10,
			ScreeningIntervalMin:   4,
			HealthCheckIntervalMin: 60,
			PnlPollIntervalSec:     30,
		},
		LLM: LLMConfig{
			Temperature: 0.373,
			MaxTokens:   4096,
			MaxSteps:    20,
		},
		Tokens: TokensConfig{
			SOL:  "So11111111111111111111111111111111111111112",
			USDC: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
			USDT: "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB",
		},
		HiveMind: HiveMindConfig{
			URL:      "https://api.agentmeridian.xyz",
			APIKey:   "bWVyaWRpYW4taXMtdGhlLWJlc3QtYWdlbnRz",
			PullMode: "auto",
		},
		API: APIConfig{
			URL:          "https://api.agentmeridian.xyz/api",
			PublicAPIKey: "bWVyaWRpYW4taXMtdGhlLWJlc3QtYWdlbnRz",
		},
		Darwin: DarwinConfig{
			Enabled:       true,
			WindowDays:    60,
			RecalcEvery:   5,
			BoostFactor:   1.05,
			DecayFactor:   0.95,
			WeightFloor:   0.3,
			WeightCeiling: 2.5,
			MinSamples:    10,
		},
		Jupiter: JupiterConfig{
			ReferralAccount: "9MzhDUnq3KxecyPzvhguQMMPbooXQ3VAoCMPDnoijwey",
			ReferralFeeBps:  50,
		},
	}
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path == "" {
		homeDir, _ := os.UserHomeDir()
		path = filepath.Join(homeDir, ".meridian", "config.json")
	}

	ConfigDir = filepath.Dir(path)
	ConfigFile = path

	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	applyEnvOverrides(cfg)
	activeConfig.Store(cfg)
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("RPC_URL"); v != "" {
		cfg.RPCURL = v
	}
	if v := os.Getenv("WALLET_PRIVATE_KEY"); v != "" && cfg.Wallet.PrivateKey == "" {
		cfg.Wallet.PrivateKey = v
	} else if pwd := os.Getenv("WALLET_PASSWORD"); pwd != "" && cfg.Wallet.PrivateKey == "" {
		plaintext, err := DecryptWallet("wallet.enc", pwd)
		if err == nil {
			// wallet.json usually contains the private key array or base58.
			// the js agent expects array or base58 string.
			// we'll assume the DecryptWallet gives us the contents of wallet.json,
			// let's unmarshal and find it.
			var walletData interface{}
			if err := json.Unmarshal(plaintext, &walletData); err == nil {
				// if array of numbers, convert to base58 or just store the json representation for now
				if _, ok := walletData.([]interface{}); ok {
					cfg.Wallet.PrivateKey = string(plaintext)
				} else if m, ok := walletData.(map[string]interface{}); ok {
					if pk, exists := m["privateKey"].(string); exists {
						cfg.Wallet.PrivateKey = pk
					}
				}
			}
		}
	}
	if v := os.Getenv("LLM_MODEL"); v != "" && cfg.LLM.ManagementModel == "" {
		cfg.LLM.ManagementModel = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		os.Setenv("LLM_BASE_URL", v)
	}
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		os.Setenv("LLM_API_KEY", v)
	}
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" && os.Getenv("LLM_API_KEY") == "" {
		os.Setenv("LLM_API_KEY", v)
	}
	if v := os.Getenv("DRY_RUN"); v == "true" {
		cfg.DryRun = true
	}
	if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" && cfg.Telegram.BotToken == "" {
		cfg.Telegram.BotToken = v
	}
	if v := os.Getenv("TELEGRAM_CHAT_ID"); v != "" && cfg.Telegram.ChatID == "" {
		cfg.Telegram.ChatID = v
	}
	if v := os.Getenv("TELEGRAM_ALLOWED_USER_IDS"); v != "" && len(cfg.Telegram.AllowedUsers) == 0 {
		parts := strings.Split(v, ",")
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				cfg.Telegram.AllowedUsers = append(cfg.Telegram.AllowedUsers, trimmed)
			}
		}
	}
	if v := os.Getenv("HELIUS_API_KEY"); v != "" {
		os.Setenv("HELIUS_API_KEY", v)
	}
	if cfg.WalletAddr == "" && cfg.Wallet.PrivateKey != "" {
		cfg.WalletAddr = deriveWalletAddress(cfg.Wallet.PrivateKey)
	}
}

func deriveWalletAddress(privateKeyStr string) string {
	if privateKeyStr == "" {
		return ""
	}
	var seed []byte
	// Try parsing as JSON array
	if err := json.Unmarshal([]byte(privateKeyStr), &seed); err != nil {
		// Try parsing as base58
		if decoded, err := base58.Decode(privateKeyStr); err == nil {
			seed = decoded
		}
	}
	if len(seed) == 64 {
		priv := ed25519.PrivateKey(seed)
		pub := priv.Public().(ed25519.PublicKey)
		return base58.Encode(pub)
	} else if len(seed) == 32 {
		priv := ed25519.NewKeyFromSeed(seed)
		pub := priv.Public().(ed25519.PublicKey)
		return base58.Encode(pub)
	}
	return ""
}

func Get() *Config {
	val := activeConfig.Load()
	if val == nil {
		return nil
	}
	return val.(*Config)
}

func (c *Config) RPCURLOrDefault() string {
	if c.RPCURL != "" {
		return c.RPCURL
	}
	return "https://mainnet.helius-rpc.com/?api-key=YOUR_KEY"
}

func (c *Config) WalletAddress() string {
	if c.WalletAddr != "" {
		return c.WalletAddr
	}
	return os.Getenv("WALLET_ADDRESS")
}

func Set(cfg *Config) {
	activeConfig.Store(cfg)
}

func (c *Config) DataPath(filename string) string {
	dir := c.DataDir
	if dir == "" {
		dir = ConfigDir
	}
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, filename)
}

func ComputeMinOpenBalance(cfg *Config) float64 {
	floor := cfg.Management.MinSolToOpen
	if floor == 0 {
		floor = 0.5
	}
	deploy := cfg.Management.DeployAmountSol
	if deploy == 0 {
		deploy = 0.4
	}
	gas := cfg.Management.GasReserve
	if gas == 0 {
		gas = 0.08
	}
	result := floor
	alt := deploy + gas
	if alt > result {
		result = alt
	}
	return float64(int(result*100)) / 100
}

func ComputeDeployAmount(walletSol float64, cfg *Config) float64 {
	reserve := cfg.Management.GasReserve
	pct := cfg.Management.PositionSizePct
	floor := cfg.Management.DeployAmountSol
	ceil := cfg.Risk.MaxDeployAmount

	deployable := walletSol - reserve
	if deployable < 0 {
		deployable = 0
	}
	result := deployable * pct
	if result < floor {
		result = floor
	}
	if result > ceil {
		result = ceil
	}
	return float64(int(result*100)) / 100
}

func HotReload() error {
	cfg := Get()
	if cfg == nil {
		cfg = DefaultConfig()
	}
	newCfg, err := LoadConfig(ConfigFile)
	if err != nil {
		return err
	}
	newCfg.DataDir = cfg.DataDir
	Set(newCfg)
	return nil
}

func SaveConfig(cfg *Config) error {
	bytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigFile, bytes, 0644)
}

