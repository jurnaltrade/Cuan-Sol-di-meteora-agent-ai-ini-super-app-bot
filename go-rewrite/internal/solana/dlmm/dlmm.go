package dlmm

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"meridian-go-rewrite/internal/config"
)

type Position struct {
	Position          string   `json:"position"`
	Pool              string   `json:"pool"`
	Pair              string   `json:"pair"`
	LowerBin          int      `json:"lower_bin"`
	UpperBin          int      `json:"upper_bin"`
	PnLUSD            float64  `json:"pnl_usd"`
	PnLPct            float64  `json:"pnl_pct"`
	InRange           bool     `json:"in_range"`
	UnclaimedFeesUSD  float64  `json:"unclaimed_fees_usd"`
	TotalValueUSD     float64  `json:"total_value_usd"`
	CollectedFeesUSD  float64  `json:"collected_fees_usd"`
	AgeMinutes        *int     `json:"age_minutes"`
	ActiveBin         *int     `json:"active_bin"`
	MinutesOutOfRange *int     `json:"minutes_out_of_range"`
	Instruction       *string  `json:"instruction"`
	FeePerTvl24h      *float64 `json:"fee_per_tvl_24h"`
}

type GetMyPositionsResult struct {
	Positions []Position `json:"positions"`
}

type Client struct {
	WalletAddress string
	RPC           string
}

func NewClient(walletAddress string, rpc string) *Client {
	return &Client{
		WalletAddress: walletAddress,
		RPC:           rpc,
	}
}

func execNodeScript(funcName string, args map[string]any) ([]byte, error) {
	script := fmt.Sprintf(`
		import { %s } from '../tools/dlmm.js';
		const args = JSON.parse(process.argv[1]);
		%s(args).then(res => { console.log(JSON.stringify(res)); process.exit(0); }).catch(err => { console.error(err.message); process.exit(1); });
	`, funcName, funcName)

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "node", "--no-warnings", "--experimental-modules", "-e", script, string(argsJSON))
	cmd.Dir = filepath.Join("..", "go-rewrite") // adjust if needed
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("node command timed out after 45s: %w", err)
		}
		return nil, fmt.Errorf("node error: %s", string(out))
	}

	// Filter out console log prefix/lines that are not JSON
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{\"") && strings.HasSuffix(trimmed, "}") {
			return []byte(trimmed), nil
		}
	}

	return out, nil
}

func (c *Client) GetMyPositions(includeStandard bool) (*GetMyPositionsResult, error) {
	out, err := execNodeScript("getMyPositions", map[string]any{"include_standard": includeStandard})
	if err != nil {
		return nil, err
	}
	var res GetMyPositionsResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) GetActiveBin(poolAddr string) (map[string]any, error) {
	out, err := execNodeScript("getActiveBin", map[string]any{"pool_address": poolAddr})
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) GetPositionPnl(poolAddr, posAddr string) (map[string]any, error) {
	out, err := execNodeScript("getPositionPnl", map[string]any{"pool_address": poolAddr, "position_address": posAddr})
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) GetPoolDetail(poolAddr string) (map[string]any, error) {
	out, err := execNodeScript("getPoolDetail", map[string]any{"pool_address": poolAddr})
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return res, nil
}

type DeployInput struct {
	PoolAddress  string  `json:"pool_address"`
	AmountY      float64 `json:"amount_y"`
	AmountX      float64 `json:"amount_x"`
	Strategy     string  `json:"strategy"`
	BinsBelow    int     `json:"bins_below"`
	BinsAbove    int     `json:"bins_above"`
	PoolName     string  `json:"pool_name"`
	BaseMint     string  `json:"base_mint"`
	BinStep      int     `json:"bin_step"`
	BaseFee      float64 `json:"base_fee"`
	Volatility   float64 `json:"volatility"`
	FeeTVLRatio  float64 `json:"fee_tvl_ratio"`
	OrganicScore float64 `json:"organic_score"`
}

type DeployResult struct {
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
	Position string `json:"position,omitempty"`
	Pool     string `json:"pool,omitempty"`
	PoolName string `json:"pool_name,omitempty"`
}

func DeployPosition(input DeployInput, cfg *config.Config) (*DeployResult, error) {
	b, _ := json.Marshal(input)
	var args map[string]any
	json.Unmarshal(b, &args)

	out, err := execNodeScript("deployPosition", args)
	if err != nil {
		return nil, err
	}
	var res DeployResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

type CloseResult struct {
	Success  bool    `json:"success"`
	Error    string  `json:"error,omitempty"`
	Pool     string  `json:"pool,omitempty"`
	PoolName string  `json:"pool_name,omitempty"`
	Position string  `json:"position,omitempty"`
	PnLPct   float64 `json:"pnl_pct"`
	PnLUSD   float64 `json:"pnl_usd"`
}

func ClosePosition(addr string, reason string, skipSwap bool, cfg *config.Config) (*CloseResult, error) {
	out, err := execNodeScript("closePosition", map[string]any{"position_address": addr, "reason": reason, "skip_swap": skipSwap})
	if err != nil {
		return nil, err
	}
	var res CloseResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func ClaimFees(addr string, cfg *config.Config) (map[string]any, error) {
	out, err := execNodeScript("claimFees", map[string]any{"position_address": addr})
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return res, nil
}

func SearchPools(query string, limit int) ([]map[string]any, error) {
	out, err := execNodeScript("searchPools", map[string]any{"query": query, "limit": limit})
	if err != nil {
		return nil, err
	}
	var res struct {
		Pools []map[string]any `json:"pools"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return res.Pools, nil
}
