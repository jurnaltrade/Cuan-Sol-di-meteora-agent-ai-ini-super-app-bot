package agent

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"meridian-go-rewrite/internal/api"
	"meridian-go-rewrite/internal/config"
	"meridian-go-rewrite/internal/logger"
	"meridian-go-rewrite/internal/registry"
	"meridian-go-rewrite/internal/screening"
	"meridian-go-rewrite/internal/signal"
	"meridian-go-rewrite/internal/solana"
	"meridian-go-rewrite/internal/solana/dlmm"
	"meridian-go-rewrite/internal/solana/jupiter"
	"meridian-go-rewrite/internal/solana/types"
	"meridian-go-rewrite/internal/telegram"
)

var SCREENER_TOOLS = []string{
	"deploy_position", "get_active_bin", "get_top_candidates",
	"check_smart_wallets_on_pool", "get_token_holders", "get_token_narrative",
	"get_token_info", "search_pools", "get_pool_memory", "get_wallet_balance",
	"get_my_positions", "get_pool_detail", "discover_pools",
}

var MANAGER_TOOLS = []string{
	"close_position", "claim_fees", "swap_token",
	"get_position_pnl", "get_my_positions", "get_wallet_balance",
	"get_recent_decisions", "get_pool_memory",
}

var (
	ToolDLMMClient *dlmm.Client
	ToolWalletAddr string
)

func getToolsForRole(agentType string) []ToolDef {
	var toolNames []string
	switch agentType {
	case "MANAGER":
		toolNames = MANAGER_TOOLS
	case "SCREENER":
		toolNames = SCREENER_TOOLS
	default:
		toolNames = AllToolNames()
	}

	defs := GetAllToolDefinitions()
	result := make([]ToolDef, 0)
	for _, d := range defs {
		for _, name := range toolNames {
			if d.Function.Name == name {
				result = append(result, d)
				break
			}
		}
	}
	return result
}

func AllToolNames() []string {
	all := make([]string, 0, len(GetAllToolDefinitions()))
	for _, d := range GetAllToolDefinitions() {
		all = append(all, d.Function.Name)
	}
	return all
}

func SetToolDeps(walletAddr string, rpcURL string) {
	ToolWalletAddr = walletAddr
	if rpcURL != "" {
		ToolDLMMClient = dlmm.NewClient(walletAddr, rpcURL)
	}
}

func ExecuteToolForCLI(name string, args map[string]any, cfg *config.Config) any {
	if ToolWalletAddr == "" {
		ToolWalletAddr = cfg.WalletAddress()
	}
	if ToolDLMMClient == nil {
		ToolDLMMClient = dlmm.NewClient(ToolWalletAddr, cfg.RPCURLOrDefault())
	}
	return executeTool(name, args, cfg)
}

func executeTool(name string, args map[string]any, cfg *config.Config) any {
	switch name {
	case "get_wallet_balance":
		return execGetWalletBalance(cfg)
	case "get_my_positions":
		return execGetMyPositions(cfg)
	case "get_top_candidates":
		return execGetTopCandidates(cfg)
	case "get_active_bin":
		return execGetActiveBin(args, cfg)
	case "deploy_position":
		return execDeploy(args, cfg)
	case "close_position":
		return execClose(args, cfg)
	case "claim_fees":
		return execClaimFees(args, cfg)
	case "swap_token":
		return execSwap(args, cfg)
	case "get_token_info":
		return execGetTokenInfo(args)
	case "get_token_narrative":
		return execGetTokenNarrative(args)
	case "get_pool_memory":
		return execGetPoolMemory(args)
	case "check_smart_wallets_on_pool":
		return execCheckSmartWallets(args, cfg)
	case "get_token_holders":
		return execGetTokenHolders(args)
	case "search_pools":
		return execSearchPools(args)
	case "get_position_pnl":
		return execGetPositionPnl(args, cfg)
	case "get_wallet_positions":
		return execGetWalletPositions(args)
	case "update_config":
		return execUpdateConfig(args, cfg)
	case "get_recent_decisions":
		return execGetRecentDecisions(args)
	case "get_pool_detail":
		return execGetPoolDetail(args, cfg)
	case "discover_pools":
		return execDiscoverPools(cfg)
	case "get_performance_history":
		return execGetPerformanceHistory(args)
	case "list_lessons":
		return execListLessons()
	case "list_strategies":
		return execListStrategies()
	case "get_strategy":
		return execGetStrategy(args)
	case "set_active_strategy":
		return execSetActiveStrategy(args)
	case "add_to_blacklist":
		return execAddToBlacklist(args)
	case "list_blacklist":
		return execListBlacklist()
	case "list_smart_wallets":
		return execListSmartWallets()
	case "add_smart_wallet":
		return execAddSmartWallet(args)
	case "remove_smart_wallet":
		return execRemoveSmartWallet(args)
	case "get_signal_weights":
		return execGetSignalWeights()
	case "study_top_lpers":
		return execStudyTopLpers(args)
	default:
		return map[string]any{"success": false, "error": "tool not implemented: " + name}
	}
}

func execGetWalletBalance(cfg *config.Config) map[string]any {
	balances, err := solana.GetHeliusBalances(ToolWalletAddr, cfg.Tokens.SOL, cfg.Tokens.USDC)
	if err != nil {
		return map[string]any{
			"sol": 0.0, "sol_price": 0.0, "sol_usd": 0.0,
			"usdc": 0.0, "tokens": []any{}, "total_usd": 0.0,
			"error": err.Error(),
		}
	}
	return map[string]any{
		"sol":       balances.SOL,
		"sol_price": balances.SOLPrice,
		"sol_usd":   balances.SOLUSD,
		"usdc":      balances.USDC,
		"tokens":    balances.Tokens,
		"total_usd": balances.TotalUSD,
	}
}

func execGetMyPositions(cfg *config.Config) any {
	_ = cfg
	if ToolDLMMClient == nil {
		return map[string]any{"total_positions": 0, "positions": []any{}, "error": "DLMM client not initialized"}
	}
	positions, err := ToolDLMMClient.GetMyPositions(false)
	if err != nil {
		return map[string]any{"total_positions": 0, "positions": []any{}, "error": err.Error()}
	}
	return positions
}

func execGetTopCandidates(cfg *config.Config) any {
	limit := 10

	result, err := screening.DiscoverAndScore(cfg)
	if err != nil {
		return map[string]any{"candidates": []any{}, "total_screened": 0, "note": err.Error()}
	}

	if registry.SignalWeights != nil {
		weights := registry.SignalWeights.GetWeights()
		for i := range result.Candidates {
			result.Candidates[i].ApplyWeights(weights)
		}
		for i := range result.Candidates {
			for j := i + 1; j < len(result.Candidates); j++ {
				if result.Candidates[j].WeightedScore > result.Candidates[i].WeightedScore {
					result.Candidates[i], result.Candidates[j] = result.Candidates[j], result.Candidates[i]
				}
			}
		}
	}

	if len(result.Candidates) > limit {
		result.Candidates = result.Candidates[:limit]
	}

	type candidateBrief struct {
		Pool        string  `json:"pool"`
		Name        string  `json:"name"`
		TokenX      string  `json:"token_x"`
		TokenXMint  string  `json:"token_x_mint"`
		TokenY      string  `json:"token_y"`
		FeePct      float64 `json:"base_fee_percentage"`
		TVL         float64 `json:"tvl"`
		Volume      float64 `json:"volume"`
		FeeTVLRatio float64 `json:"fee_tvl_ratio"`
		Volatility  float64 `json:"volatility"`
		BinStep     int     `json:"bin_step"`
		Holders     int     `json:"holders"`
		MCap        float64 `json:"mcap"`
		Organic     float64 `json:"organic_score"`
		ActivePos   int     `json:"active_positions"`
		Score       float64 `json:"score"`
		WScore      float64 `json:"weighted_score"`
		Summary     string  `json:"summary"`
	}

	briefs := make([]candidateBrief, len(result.Candidates))
	for i, c := range result.Candidates {
		briefs[i] = candidateBrief{
			Pool:        c.PoolAddress,
			Name:        c.Name,
			TokenX:      c.TokenX,
			TokenXMint:  c.TokenXMint,
			TokenY:      c.TokenY,
			FeePct:      c.FeePct,
			TVL:         c.TVL,
			Volume:      c.Volume,
			FeeTVLRatio: c.FeeTVLRatio,
			Volatility:  c.Volatility,
			BinStep:     c.BinStep,
			Holders:     c.Holders,
			MCap:        c.MCap,
			Organic:     c.OrganicScore,
			ActivePos:   c.ActivePositions,
			Score:       solana.Round(c.Score, 1),
			WScore:      solana.Round(c.WeightedScore, 1),
			Summary:     c.Summary(),
		}
	}

	return map[string]any{
		"candidates":     briefs,
		"total_screened": result.TotalScreened,
		"total_passed":   result.TotalPassed,
	}
}

func execGetActiveBin(args map[string]any, cfg *config.Config) any {
	_ = cfg
	poolAddr := getStringArg(args, "pool_address")
	if poolAddr == "" {
		return map[string]any{"error": "pool_address required"}
	}
	if ToolDLMMClient == nil {
		return map[string]any{"error": "DLMM client not initialized"}
	}
	bin, err := ToolDLMMClient.GetActiveBin(poolAddr)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return bin
}

func execDeploy(args map[string]any, cfg *config.Config) any {
	input := dlmm.DeployInput{
		PoolAddress:  getStringArg(args, "pool_address"),
		AmountY:      getFloatArg(args, "amount_y", cfg.Management.DeployAmountSol),
		AmountX:      getFloatArg(args, "amount_x", 0),
		Strategy:     getStringArg(args, "strategy", cfg.Strategy.Strategy),
		BinsBelow:    getIntArg(args, "bins_below", cfg.Strategy.DefaultBinsBelow),
		BinsAbove:    getIntArg(args, "bins_above", 0),
		PoolName:     getStringArg(args, "pool_name"),
		BaseMint:     getStringArg(args, "base_mint"),
		BinStep:      getIntArg(args, "bin_step", 100),
		Volatility:   getFloatArg(args, "volatility", 0),
		FeeTVLRatio:  getFloatArg(args, "fee_tvl_ratio", 0),
		OrganicScore: getFloatArg(args, "organic_score", 0),
	}
	result, err := dlmm.DeployPosition(input, cfg)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}

	if result.Success {
		var bal float64
		if balances, err := solana.GetHeliusBalances(ToolWalletAddr, cfg.Tokens.SOL, cfg.Tokens.USDC); err == nil {
			bal = balances.SOL
		}
		telegram.NotifyDeploy(result.PoolName, input.AmountY, result.Position, input.Strategy, input.BinsBelow, input.BinsAbove, bal)
	}

	if registry.SignalTracker != nil && result.Pool != "" {
		registry.SignalTracker.Stage(screeningToStaged(input, result))
	}

	return result
}

func execClose(args map[string]any, cfg *config.Config) any {
	addr := getStringArg(args, "position_address")
	reason := getStringArg(args, "reason", "")
	skipSwap := getBoolArg(args, "skip_swap", false)
	result, err := dlmm.ClosePosition(addr, reason, skipSwap, cfg)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}

	if result.Success {
		// Auto-swap base token back to SOL unless user said to hold or skip_swap is true
		if !skipSwap && result.BaseMint != "" {
			if balances, err := solana.GetHeliusBalances(ToolWalletAddr, cfg.Tokens.SOL, cfg.Tokens.USDC); err == nil {
				var tokenToSwap *solana.TokenBalance
				for _, t := range balances.Tokens {
					if t.Mint == result.BaseMint {
						tokenToSwap = &t
						break
					}
				}
				if tokenToSwap != nil && tokenToSwap.USD >= 0.10 {
					logger.Log("executor", fmt.Sprintf("Auto-swapping %s ($%.2f) back to SOL", tokenToSwap.Symbol, tokenToSwap.USD))
					swapRes, err := dlmm.SwapToken(result.BaseMint, "SOL", tokenToSwap.Balance, cfg)
					if err == nil && swapRes.Success {
						result.AutoSwapped = true
						result.AutoSwapNote = fmt.Sprintf("Base token already auto-swapped back to SOL (%s -> SOL). Do NOT call swap_token again.", tokenToSwap.Symbol)
						result.SolReceived = swapRes.AmountOut
					} else {
						errMsg := ""
						if err != nil {
							errMsg = err.Error()
						} else {
							errMsg = swapRes.Error
						}
						logger.Warn("executor", fmt.Sprintf("Auto-swap after close failed: %s", errMsg))
					}
				}
			}
		}

		var bal float64
		if balances, err := solana.GetHeliusBalances(ToolWalletAddr, cfg.Tokens.SOL, cfg.Tokens.USDC); err == nil {
			bal = balances.SOL
		}
		telegram.NotifyClose(result.PoolName, result.PnLUSD, result.PnLPct, reason, 0.0, bal)
	}

	if registry.DecisionLog != nil && result.Success {
		registry.DecisionLog.Append(types.Decision{
			ID:       "",
			Type:     "close",
			Actor:    "MANAGER",
			Pool:     result.Pool,
			PoolName: result.PoolName,
			Position: result.Position,
			Reason:   reason,
		})
	}
	if registry.PoolMemory != nil && result.Success && result.Pool != "" {
		registry.PoolMemory.RecordDeploy(result.Pool, types.DeployRecord{
			ClosedAt:    "",
			PnLPct:      result.PnLPct,
			PnLUSD:      result.PnLUSD,
			CloseReason: reason,
		})
	}

	return result
}

func execClaimFees(args map[string]any, cfg *config.Config) any {
	addr := getStringArg(args, "position_address")
	result, err := dlmm.ClaimFees(addr, cfg)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return result
}

func execSwap(args map[string]any, cfg *config.Config) map[string]any {
	inputMint := getStringArg(args, "input_mint")
	outputMint := getStringArg(args, "output_mint")
	amount := getFloatArg(args, "amount", 0)
	if inputMint == "" || outputMint == "" || amount <= 0 {
		return map[string]any{"success": false, "error": "input_mint, output_mint, and amount required"}
	}
	res, err := dlmm.SwapToken(inputMint, outputMint, amount, cfg)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if !res.Success {
		return map[string]any{"success": false, "error": res.Error}
	}
	return map[string]any{
		"success":     true,
		"tx":          res.Tx,
		"input_mint":  res.InputMint,
		"output_mint": res.OutputMint,
		"amount_in":   res.AmountIn,
		"amount_out":  res.AmountOut,
	}
}

func execGetTokenInfo(args map[string]any) map[string]any {
	query := getStringArg(args, "query")
	if query == "" {
		return map[string]any{"error": "query required"}
	}
	tokens, err := jupiter.FetchTokenInfo(query)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"query": query, "results": tokens, "count": len(tokens)}
}

func execGetTokenNarrative(args map[string]any) any {
	mint := getStringArg(args, "mint")
	if mint == "" {
		return map[string]any{"error": "mint required"}
	}
	narr, err := jupiter.FetchTokenNarrative(mint)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return narr
}

func execGetPoolMemory(args map[string]any) map[string]any {
	poolAddr := getStringArg(args, "pool_address")
	if poolAddr == "" {
		return map[string]any{"error": "pool_address required"}
	}
	if registry.PoolMemory == nil {
		return map[string]any{"pool": poolAddr, "memory": "", "note": "persistence not available"}
	}
	memory := registry.PoolMemory.RecallForPool(poolAddr)
	return map[string]any{"pool": poolAddr, "memory": memory}
}

func execCheckSmartWallets(args map[string]any, cfg *config.Config) map[string]any {
	poolAddr := getStringArg(args, "pool_address")
	if poolAddr == "" {
		return map[string]any{"error": "pool_address required"}
	}
	if registry.SmartWallets == nil {
		return map[string]any{"pool": poolAddr, "matching": []string{}, "total": 0, "note": "smart wallets not configured"}
	}
	wallets := registry.SmartWallets.List()
	if len(wallets) == 0 {
		return map[string]any{"pool": poolAddr, "matching": []string{}, "total": 0, "note": "no smart wallets registered"}
	}

	matching := make([]map[string]any, 0)
	for _, w := range wallets {
		tmpClient := dlmm.NewClient(w.Address, cfg.RPCURLOrDefault())
		positions, err := tmpClient.GetMyPositions(false)
		if err != nil {
			continue
		}
		for _, p := range positions.Positions {
			if strings.EqualFold(p.Pool, poolAddr) {
				matching = append(matching, map[string]any{
					"wallet":    w.Name,
					"address":   w.Address,
					"position":  p.Position,
					"pair":      p.Pair,
					"pnl_usd":   p.PnLUSD,
					"pnl_pct":   p.PnLPct,
					"in_range":  p.InRange,
					"unclaimed": p.UnclaimedFeesUSD,
				})
			}
		}
	}
	return map[string]any{"pool": poolAddr, "matching": matching, "total": len(matching)}
}

func execGetTokenHolders(args map[string]any) map[string]any {
	mint := getStringArg(args, "mint")
	limit := getIntArg(args, "limit", 100)
	if mint == "" {
		return map[string]any{"error": "mint required"}
	}
	holders, total, err := solana.GetHeliusTokenHolders(mint, limit)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"mint": mint, "holders": holders, "total_holders": total}
}

func execSearchPools(args map[string]any) map[string]any {
	query := getStringArg(args, "query")
	limit := getIntArg(args, "limit", 10)
	if query == "" {
		return map[string]any{"error": "query required"}
	}
	pools, err := dlmm.SearchPools(query, limit)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"results": pools, "count": len(pools)}
}

func execGetPositionPnl(args map[string]any, cfg *config.Config) any {
	_ = cfg
	poolAddr := getStringArg(args, "pool_address")
	posAddr := getStringArg(args, "position_address")
	if poolAddr == "" || posAddr == "" {
		return map[string]any{"error": "pool_address and position_address required"}
	}
	if ToolDLMMClient == nil {
		return map[string]any{"error": "DLMM client not initialized"}
	}
	pnl, err := ToolDLMMClient.GetPositionPnl(poolAddr, posAddr)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return pnl
}

func execGetWalletPositions(args map[string]any) any {
	walletAddr := getStringArg(args, "wallet_address")
	if walletAddr == "" {
		return map[string]any{"error": "wallet_address required"}
	}
	tmpClient := dlmm.NewClient(walletAddr, os.Getenv("RPC_URL"))
	positions, err := tmpClient.GetMyPositions(true)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return positions
}

func execUpdateConfig(args map[string]any, cfg *config.Config) map[string]any {
	changes := getStringArg(args, "changes")
	if changes == "" {
		return map[string]any{"success": false, "error": "changes required"}
	}

	err := config.HotReload()
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "note": "config reloaded from file", "changes": changes}
}

func execGetRecentDecisions(args map[string]any) map[string]any {
	limit := getIntArg(args, "limit", 20)
	if registry.DecisionLog == nil {
		return map[string]any{"decisions": []any{}, "count": 0, "note": "decision log not available"}
	}
	decisions := registry.DecisionLog.GetRecent(limit)
	summary := registry.DecisionLog.GetDecisionSummary(limit)
	return map[string]any{"decisions": decisions, "count": len(decisions), "summary": summary}
}

func execGetPoolDetail(args map[string]any, cfg *config.Config) any {
	_ = cfg
	poolAddr := getStringArg(args, "pool_address")
	if poolAddr == "" {
		return map[string]any{"error": "pool_address required"}
	}
	if ToolDLMMClient == nil {
		return map[string]any{"error": "DLMM client not initialized"}
	}
	detail, err := ToolDLMMClient.GetPoolDetail(poolAddr)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return detail
}

func execDiscoverPools(cfg *config.Config) map[string]any {
	result, err := screening.DiscoverAndScore(cfg)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	type brief struct {
		Pool        string  `json:"pool"`
		Name        string  `json:"name"`
		TokenX      string  `json:"token_x"`
		TVL         float64 `json:"tvl"`
		Volume      float64 `json:"volume"`
		FeeTVLRatio float64 `json:"fee_tvl_ratio"`
		Volatility  float64 `json:"volatility"`
		Score       float64 `json:"score"`
	}
	briefs := make([]brief, 0, len(result.Candidates))
	for _, c := range result.Candidates {
		briefs = append(briefs, brief{
			Pool: c.PoolAddress, Name: c.Name, TokenX: c.TokenX,
			TVL: c.TVL, Volume: c.Volume, FeeTVLRatio: c.FeeTVLRatio,
			Volatility: c.Volatility, Score: c.Score,
		})
	}
	return map[string]any{
		"pools":          briefs,
		"total_fetched":  result.TotalFetched,
		"total_screened": result.TotalScreened,
		"total_passed":   result.TotalPassed,
	}
}

func execGetPerformanceHistory(args map[string]any) map[string]any {
	limit := getIntArg(args, "limit", 50)
	if registry.Lessons == nil {
		return map[string]any{"performance": []any{}, "count": 0, "note": "not available"}
	}
	perf := registry.Lessons.GetRecentPerformance(limit)
	summary := registry.Lessons.GetPerformanceSummary()
	return map[string]any{"performance": perf, "count": len(perf), "summary": summary}
}

func execListLessons() map[string]any {
	if registry.Lessons == nil {
		return map[string]any{"lessons": "", "note": "not available"}
	}
	lessons := registry.Lessons.GetLessonsForPrompt("SCREENER", 20)
	return map[string]any{"lessons": lessons}
}

func execListStrategies() map[string]any {
	if registry.Strategies == nil {
		return map[string]any{"strategies": []any{}, "note": "not available"}
	}
	strategies := registry.Strategies.ListStrategies()
	return map[string]any{"strategies": strategies, "count": len(strategies)}
}

func execGetStrategy(args map[string]any) map[string]any {
	if registry.Strategies == nil {
		return map[string]any{"error": "not available"}
	}
	id := getStringArg(args, "id")
	if id == "" {
		active := registry.Strategies.GetActiveStrategy()
		if active == nil {
			return map[string]any{"error": "no active strategy"}
		}
		return map[string]any{"strategy": active}
	}
	strategies := registry.Strategies.ListStrategies()
	for _, s := range strategies {
		if s.ID == id {
			return map[string]any{"strategy": s}
		}
	}
	return map[string]any{"error": "strategy not found"}
}

func execSetActiveStrategy(args map[string]any) map[string]any {
	if registry.Strategies == nil {
		return map[string]any{"error": "not available"}
	}
	id := getStringArg(args, "id")
	if id == "" {
		return map[string]any{"error": "id required"}
	}
	if err := registry.Strategies.SetActive(id); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "active": id}
}

func execAddToBlacklist(args map[string]any) map[string]any {
	if registry.TokenBl == nil {
		return map[string]any{"error": "not available"}
	}
	mint := getStringArg(args, "mint")
	symbol := getStringArg(args, "symbol")
	reason := getStringArg(args, "reason")
	if mint == "" {
		return map[string]any{"error": "mint required"}
	}
	registry.TokenBl.Add(mint, symbol, reason)
	return map[string]any{"success": true, "mint": mint, "symbol": symbol}
}

func execListBlacklist() map[string]any {
	if registry.TokenBl == nil {
		return map[string]any{"blacklist": []any{}, "note": "not available"}
	}
	data := registry.TokenBl.IsBlacklisted("") // trigger read
	_ = data
	return map[string]any{"blacklist": []any{}, "note": "blacklist store active"}
}

func execListSmartWallets() map[string]any {
	if registry.SmartWallets == nil {
		return map[string]any{"wallets": []any{}, "note": "not available"}
	}
	wallets := registry.SmartWallets.List()
	return map[string]any{"wallets": wallets, "count": len(wallets)}
}

func execAddSmartWallet(args map[string]any) map[string]any {
	if registry.SmartWallets == nil {
		return map[string]any{"error": "not available"}
	}
	address := getStringArg(args, "address")
	name := getStringArg(args, "name")
	if address == "" {
		return map[string]any{"error": "address required"}
	}
	registry.SmartWallets.Add(types.SmartWallet{
		Name:    name,
		Address: address,
		Type:    getStringArg(args, "type", "lper"),
	})
	return map[string]any{"success": true, "address": address, "name": name}
}

func execRemoveSmartWallet(args map[string]any) map[string]any {
	if registry.SmartWallets == nil {
		return map[string]any{"error": "not available"}
	}
	address := getStringArg(args, "address")
	if address == "" {
		return map[string]any{"error": "address required"}
	}
	registry.SmartWallets.Remove(address)
	return map[string]any{"success": true, "address": address}
}

func execGetSignalWeights() map[string]any {
	if registry.SignalWeights == nil {
		return map[string]any{"weights": map[string]float64{}, "note": "not available"}
	}
	weights := registry.SignalWeights.GetWeights()
	summary := registry.SignalWeights.GetSummary()
	return map[string]any{"weights": weights, "summary": summary}
}

func execStudyTopLpers(args map[string]any) map[string]any {
	poolAddr, _ := args["pool_address"].(string)
	limitF, _ := args["limit"].(float64)
	limit := 4
	if limitF > 0 {
		limit = int(limitF)
	}

	res, err := api.StudyTopLpers(poolAddr, limit)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	res["success"] = true
	return res
}

func screeningToStaged(input dlmm.DeployInput, result *dlmm.DeployResult) signal.StagedSignal {
	return signal.StagedSignal{
		Pool:         input.PoolAddress,
		PoolName:     input.PoolName,
		BaseMint:     input.BaseMint,
		BinStep:      input.BinStep,
		FeePct:       input.BaseFee,
		Volatility:   input.Volatility,
		FeeTVLRatio:  input.FeeTVLRatio,
		OrganicScore: input.OrganicScore,
	}
}

func getStringArg(args map[string]any, key string, fallback ...string) string {
	if v, ok := args[key]; ok {
		s := ""
		switch t := v.(type) {
		case string:
			s = t
		}
		if s != "" {
			return strings.TrimSpace(s)
		}
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	return ""
}

func getFloatArg(args map[string]any, key string, fallback ...float64) float64 {
	if v, ok := args[key]; ok {
		switch t := v.(type) {
		case float64:
			return t
		case string:
			f, err := strconv.ParseFloat(t, 64)
			if err == nil {
				return f
			}
		}
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	return 0
}

func getIntArg(args map[string]any, key string, fallback ...int) int {
	if v, ok := args[key]; ok {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case string:
			i, err := strconv.Atoi(t)
			if err == nil {
				return i
			}
		}
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	return 0
}

func getBoolArg(args map[string]any, key string, fallback ...bool) bool {
	if v, ok := args[key]; ok {
		switch t := v.(type) {
		case bool:
			return t
		}
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	return false
}

func GetAllToolDefinitions() []ToolDef {
	return []ToolDef{
		{Function: FunctionDef{Name: "get_wallet_balance", Description: "Get current wallet balances (SOL, USDC, all tokens) via Helius.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
		{Function: FunctionDef{Name: "get_my_positions", Description: "List all open DLMM positions for the agent wallet.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
		{Function: FunctionDef{Name: "get_top_candidates", Description: "Get top pre-scored pool candidates from Meteora Discovery API. Fully scored and filtered.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"limit": map[string]any{"type": "number", "description": "Number of candidates. Default 10."}}}}},
		{Function: FunctionDef{Name: "discover_pools", Description: "Run full pool discovery from Meteora Discovery API. Returns all pools matching screening filters.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
		{Function: FunctionDef{Name: "get_active_bin", Description: "Get current active bin and price for a DLMM pool.", Parameters: mkParams(map[string]any{"pool_address": map[string]any{"type": "string", "description": "DLMM pool address"}}, "pool_address")}},
		{Function: FunctionDef{Name: "get_pool_detail", Description: "Get full pool detail (tokens, TVL, volume, bin step, fees) from Meteora.", Parameters: mkParams(map[string]any{"pool_address": map[string]any{"type": "string"}}, "pool_address")}},
		{Function: FunctionDef{Name: "deploy_position", Description: "Open a new DLMM liquidity position. Single-side SOL only (amount_y).", Parameters: mkParams(map[string]any{
			"pool_address": map[string]any{"type": "string"}, "amount_y": map[string]any{"type": "number"},
			"amount_x": map[string]any{"type": "number"}, "strategy": map[string]any{"type": "string", "enum": []string{"bid_ask", "spot"}},
			"bins_below": map[string]any{"type": "number"}, "bins_above": map[string]any{"type": "number"},
			"pool_name": map[string]any{"type": "string"}, "base_mint": map[string]any{"type": "string"},
			"bin_step": map[string]any{"type": "number"}, "volatility": map[string]any{"type": "number"},
			"fee_tvl_ratio": map[string]any{"type": "number"}, "organic_score": map[string]any{"type": "number"},
		}, "pool_address")}},
		{Function: FunctionDef{Name: "close_position", Description: "Remove liquidity and close a position. Include rule name as reason.", Parameters: mkParams(map[string]any{
			"position_address": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"},
			"skip_swap": map[string]any{"type": "boolean"},
		}, "position_address")}},
		{Function: FunctionDef{Name: "claim_fees", Description: "Claim accumulated swap fees from a position.", Parameters: mkParams(map[string]any{"position_address": map[string]any{"type": "string"}}, "position_address")}},
		{Function: FunctionDef{Name: "swap_token", Description: "Swap tokens via Jupiter.", Parameters: mkParams(map[string]any{
			"input_mint": map[string]any{"type": "string"}, "output_mint": map[string]any{"type": "string"},
			"amount": map[string]any{"type": "number"},
		}, "input_mint", "output_mint", "amount")}},
		{Function: FunctionDef{Name: "get_token_info", Description: "Get token data from Jupiter (organic score, holders, audit, mcap).", Parameters: mkParams(map[string]any{"query": map[string]any{"type": "string"}}, "query")}},
		{Function: FunctionDef{Name: "get_token_narrative", Description: "Get narrative/story behind a token from Jupiter ChainInsight.", Parameters: mkParams(map[string]any{"mint": map[string]any{"type": "string"}}, "mint")}},
		{Function: FunctionDef{Name: "get_token_holders", Description: "Get holder distribution for a token via Helius.", Parameters: mkParams(map[string]any{
			"mint": map[string]any{"type": "string"}, "limit": map[string]any{"type": "number"},
		}, "mint")}},
		{Function: FunctionDef{Name: "search_pools", Description: "Search for DLMM pools by token symbol or name.", Parameters: mkParams(map[string]any{
			"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "number"},
		}, "query")}},
		{Function: FunctionDef{Name: "get_position_pnl", Description: "Get detailed PnL for a specific position.", Parameters: mkParams(map[string]any{
			"pool_address": map[string]any{"type": "string"}, "position_address": map[string]any{"type": "string"},
		}, "pool_address", "position_address")}},
		{Function: FunctionDef{Name: "get_wallet_positions", Description: "Get open DLMM positions for any wallet.", Parameters: mkParams(map[string]any{"wallet_address": map[string]any{"type": "string"}}, "wallet_address")}},
		{Function: FunctionDef{Name: "get_pool_memory", Description: "Check deploy history for a pool before deploying.", Parameters: mkParams(map[string]any{"pool_address": map[string]any{"type": "string"}}, "pool_address")}},
		{Function: FunctionDef{Name: "check_smart_wallets_on_pool", Description: "Check if tracked smart wallets have positions in a pool.", Parameters: mkParams(map[string]any{"pool_address": map[string]any{"type": "string"}}, "pool_address")}},
		{Function: FunctionDef{Name: "update_config", Description: "Reload config from file (hot reload).", Parameters: mkParams(map[string]any{"changes": map[string]any{"type": "object"}}, "changes")}},
		{Function: FunctionDef{Name: "get_recent_decisions", Description: "Get recent structured decision log.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"limit": map[string]any{"type": "number", "default": 20}}}}},
		{Function: FunctionDef{Name: "get_performance_history", Description: "Get closed position performance history.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"limit": map[string]any{"type": "number", "default": 50}}}}},
		{Function: FunctionDef{Name: "list_lessons", Description: "List learned lessons from past deployments.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
		{Function: FunctionDef{Name: "list_strategies", Description: "List available deployment strategies.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
		{Function: FunctionDef{Name: "get_strategy", Description: "Get strategy by id or active strategy.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}}}},
		{Function: FunctionDef{Name: "set_active_strategy", Description: "Set the active strategy.", Parameters: mkParams(map[string]any{"id": map[string]any{"type": "string"}}, "id")}},
		{Function: FunctionDef{Name: "add_to_blacklist", Description: "Add a token mint to the blacklist.", Parameters: mkParams(map[string]any{
			"mint": map[string]any{"type": "string"}, "symbol": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"},
		}, "mint")}},
		{Function: FunctionDef{Name: "list_blacklist", Description: "List blacklisted token mints.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
		{Function: FunctionDef{Name: "list_smart_wallets", Description: "List tracked smart wallets.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
		{Function: FunctionDef{Name: "add_smart_wallet", Description: "Add a wallet to smart wallet tracker.", Parameters: mkParams(map[string]any{
			"address": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
			"type": map[string]any{"type": "string", "default": "lper"},
		}, "address")}},
		{Function: FunctionDef{Name: "remove_smart_wallet", Description: "Remove a wallet from smart wallet tracker.", Parameters: mkParams(map[string]any{"address": map[string]any{"type": "string"}}, "address")}},
		{Function: FunctionDef{Name: "get_signal_weights", Description: "Get current Darwinian signal weights and summary.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
		{Function: FunctionDef{Name: "study_top_lpers", Description: "Study top LPers for a pool.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"pool_address": map[string]any{"type": "string", "description": "Pool address to study top LPers for"}, "limit": map[string]any{"type": "number", "description": "Number of top LPers to study. Default 4."}}, "required": []string{"pool_address"}}}},
	}
}

func mkParams(props map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}
