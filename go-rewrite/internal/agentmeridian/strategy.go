package agentmeridian

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"meridian-go-rewrite/internal/config"
	"meridian-go-rewrite/internal/logger"
)

type StrategyCriteria map[string]interface{}

type Strategy struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Author        string           `json:"author"`
	LpStrategy    string           `json:"lp_strategy"`
	TokenCriteria StrategyCriteria `json:"token_criteria"`
	Entry         StrategyCriteria `json:"entry"`
	Range         StrategyCriteria `json:"range"`
	Exit          StrategyCriteria `json:"exit"`
	BestFor       string           `json:"best_for"`
	Raw           string           `json:"raw,omitempty"`
	AddedAt       string           `json:"added_at"`
	UpdatedAt     string           `json:"updated_at"`
}

type StrategyLibrary struct {
	Active     string              `json:"active"`
	Strategies map[string]Strategy `json:"strategies"`
}

var (
	strategyMutex sync.RWMutex
	strategyFile  string
)

func initStrategyPath() {
	if strategyFile == "" {
		cfg := config.Get()
		if cfg != nil {
			strategyFile = cfg.DataPath("strategy-library.json")
		} else {
			strategyFile = "strategy-library.json"
		}
	}
}

func loadStrategies() StrategyLibrary {
	initStrategyPath()
	strategyMutex.RLock()
	defer strategyMutex.RUnlock()

	data, err := os.ReadFile(strategyFile)
	if err != nil {
		return StrategyLibrary{Active: "", Strategies: make(map[string]Strategy)}
	}

	var lib StrategyLibrary
	if err := json.Unmarshal(data, &lib); err != nil {
		return StrategyLibrary{Active: "", Strategies: make(map[string]Strategy)}
	}
	if lib.Strategies == nil {
		lib.Strategies = make(map[string]Strategy)
	}
	return lib
}

func saveStrategies(lib StrategyLibrary) {
	initStrategyPath()
	strategyMutex.Lock()
	defer strategyMutex.Unlock()

	if dir := filepath.Dir(strategyFile); dir != "." {
		os.MkdirAll(dir, 0755)
	}

	bytes, _ := json.MarshalIndent(lib, "", "  ")
	os.WriteFile(strategyFile, bytes, 0644)
}

func EnsureDefaultStrategies() {
	lib := loadStrategies()

	defaults := map[string]Strategy{
		"custom_ratio_spot": {
			ID:            "custom_ratio_spot",
			Name:          "Custom Ratio Spot",
			Author:        "meridian",
			LpStrategy:    "spot",
			TokenCriteria: StrategyCriteria{"notes": "Any token. Ratio expresses directional bias."},
			Entry: StrategyCriteria{
				"condition":   "Directional view on token",
				"single_side": nil,
				"notes":       "75% token = bullish (sell on pump out of range). 75% SOL = bearish/DCA-in (buy on dip). Set bins_below:bins_above proportional to ratio.",
			},
			Range:   StrategyCriteria{"type": "custom", "notes": "bins_below:bins_above ratio matches token:SOL ratio. E.g., 75% token → ~52 bins below, ~17 bins above."},
			Exit:    StrategyCriteria{"take_profit_pct": 10, "notes": "Close when OOR or TP hit. Re-deploy with updated ratio based on new momentum signals."},
			BestFor: "Expressing directional bias while earning fees both ways",
		},
		"single_sided_reseed": {
			ID:            "single_sided_reseed",
			Name:          "Single-Sided Bid-Ask + Re-seed",
			Author:        "meridian",
			LpStrategy:    "bid_ask",
			TokenCriteria: StrategyCriteria{"notes": "Volatile tokens with strong narrative. Must have active volume."},
			Entry: StrategyCriteria{
				"condition":   "Deploy token-only (amount_x only, amount_y=0) bid-ask, bins below active bin only",
				"single_side": "token",
				"notes":       "As price drops through bins, token sold for SOL. Bid-ask concentrates at bottom edge.",
			},
			Range:   StrategyCriteria{"type": "default", "bins_below_pct": 100, "notes": "All bins below active bin. bins_above=0."},
			Exit:    StrategyCriteria{"notes": "When OOR downside: close_position(skip_swap=true) → redeploy token-only bid-ask at new lower price. Do NOT swap to SOL. Full close only when token dead or after N re-seeds with declining performance."},
			BestFor: "Riding volatile tokens down without cutting losses. DCA out via LP.",
		},
	}

	added := false
	for id, s := range defaults {
		if _, exists := lib.Strategies[id]; !exists {
			s.AddedAt = time.Now().UTC().Format(time.RFC3339)
			s.UpdatedAt = s.AddedAt
			lib.Strategies[id] = s
			added = true
		}
	}

	if added {
		if lib.Active == "" {
			lib.Active = "custom_ratio_spot"
		}
		saveStrategies(lib)
		logger.Log("strategy", "Preloaded default strategies")
	}
}

var slugRe = regexp.MustCompile(`[^a-z0-9_]`)

func AddStrategy(id, name, author, lpStrategy string, tokenCriteria, entry, rng, exit StrategyCriteria, bestFor, raw string) map[string]interface{} {
	if id == "" || name == "" {
		return map[string]interface{}{"error": "id and name are required"}
	}

	lib := loadStrategies()

	slug := strings.ToLower(id)
	slug = strings.ReplaceAll(slug, " ", "_")
	slug = slugRe.ReplaceAllString(slug, "")

	if author == "" {
		author = "unknown"
	}
	if lpStrategy == "" {
		lpStrategy = "bid_ask"
	}

	strategy := Strategy{
		ID:            slug,
		Name:          name,
		Author:        author,
		LpStrategy:    lpStrategy,
		TokenCriteria: tokenCriteria,
		Entry:         entry,
		Range:         rng,
		Exit:          exit,
		BestFor:       bestFor,
		Raw:           raw,
		AddedAt:       time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}

	lib.Strategies[slug] = strategy
	if lib.Active == "" {
		lib.Active = slug
	}

	saveStrategies(lib)
	logger.Log("strategy", fmt.Sprintf("Strategy saved: %s (%s)", name, slug))

	return map[string]interface{}{
		"saved":  true,
		"id":     slug,
		"name":   name,
		"active": lib.Active == slug,
	}
}

func ListStrategies() map[string]interface{} {
	lib := loadStrategies()
	var list []map[string]interface{}
	for _, s := range lib.Strategies {
		added := "unknown"
		if len(s.AddedAt) >= 10 {
			added = s.AddedAt[:10]
		}
		list = append(list, map[string]interface{}{
			"id":          s.ID,
			"name":        s.Name,
			"author":      s.Author,
			"lp_strategy": s.LpStrategy,
			"best_for":    s.BestFor,
			"active":      lib.Active == s.ID,
			"added_at":    added,
		})
	}
	return map[string]interface{}{
		"active":     lib.Active,
		"count":      len(list),
		"strategies": list,
	}
}

func GetStrategy(id string) map[string]interface{} {
	if id == "" {
		return map[string]interface{}{"error": "id required"}
	}
	lib := loadStrategies()
	s, exists := lib.Strategies[id]
	if !exists {
		var keys []string
		for k := range lib.Strategies {
			keys = append(keys, k)
		}
		return map[string]interface{}{"error": fmt.Sprintf("Strategy %q not found", id), "available": keys}
	}

	bytes, _ := json.Marshal(s)
	var m map[string]interface{}
	json.Unmarshal(bytes, &m)
	m["is_active"] = lib.Active == id
	return m
}

func SetActiveStrategy(id string) map[string]interface{} {
	if id == "" {
		return map[string]interface{}{"error": "id required"}
	}
	lib := loadStrategies()
	if s, exists := lib.Strategies[id]; !exists {
		var keys []string
		for k := range lib.Strategies {
			keys = append(keys, k)
		}
		return map[string]interface{}{"error": fmt.Sprintf("Strategy %q not found", id), "available": keys}
	} else {
		lib.Active = id
		saveStrategies(lib)
		logger.Log("strategy", fmt.Sprintf("Active strategy set to: %s", s.Name))
		return map[string]interface{}{"active": id, "name": s.Name}
	}
}

func RemoveStrategy(id string) map[string]interface{} {
	if id == "" {
		return map[string]interface{}{"error": "id required"}
	}
	lib := loadStrategies()
	s, exists := lib.Strategies[id]
	if !exists {
		return map[string]interface{}{"error": fmt.Sprintf("Strategy %q not found", id)}
	}

	delete(lib.Strategies, id)
	if lib.Active == id {
		lib.Active = ""
		for k := range lib.Strategies {
			lib.Active = k
			break
		}
	}
	saveStrategies(lib)
	logger.Log("strategy", fmt.Sprintf("Strategy removed: %s", s.Name))
	return map[string]interface{}{
		"removed":    true,
		"id":         id,
		"name":       s.Name,
		"new_active": lib.Active,
	}
}

func GetActiveStrategy() *Strategy {
	lib := loadStrategies()
	if lib.Active == "" {
		return nil
	}
	if s, exists := lib.Strategies[lib.Active]; exists {
		return &s
	}
	return nil
}
