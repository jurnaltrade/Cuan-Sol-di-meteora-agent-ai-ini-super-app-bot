package hivemind

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"meridian-go-rewrite/internal/config"
	"meridian-go-rewrite/internal/logger"
)

var (
	userConfigPath     = "user-config.json"
	cachePath          = "hivemind-cache.json"
	heartbeatInterval  = 15 * time.Minute
	agentVersion       = "1.0.0"
	hivemindHTTPClient = &http.Client{Timeout: 30 * time.Second}
)

type SharedLesson struct {
	ID         string   `json:"id"`
	Rule       string   `json:"rule"`
	Tags       []string `json:"tags"`
	Role       string   `json:"role"`
	Outcome    string   `json:"outcome"`
	SourceType string   `json:"sourceType"`
	Score      float64  `json:"score"`
	CreatedAt  string   `json:"created_at"`
}

type HiveCache struct {
	SharedLessons []SharedLesson `json:"sharedLessons"`
	Presets       []interface{}  `json:"presets"`
	PulledAt      string         `json:"pulledAt"`
}

type UserConfig struct {
	AgentID string `json:"agentId"`
}

func readUserConfig() UserConfig {
	cfg := config.Get()
	path := userConfigPath
	if cfg != nil {
		path = cfg.DataPath(userConfigPath)
	}

	var uc UserConfig
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &uc)
	}
	return uc
}

func writeUserConfig(uc UserConfig) {
	cfg := config.Get()
	path := userConfigPath
	if cfg != nil {
		path = cfg.DataPath(userConfigPath)
	}

	if dir := filepath.Dir(path); dir != "." {
		os.MkdirAll(dir, 0755)
	}
	bytes, _ := json.MarshalIndent(uc, "", "  ")
	os.WriteFile(path, bytes, 0644)
}

func readCache() HiveCache {
	cfg := config.Get()
	path := cachePath
	if cfg != nil {
		path = cfg.DataPath(cachePath)
	}

	hc := HiveCache{
		SharedLessons: []SharedLesson{},
		Presets:       []interface{}{},
	}
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &hc)
	}
	return hc
}

func writeCache(hc HiveCache) {
	cfg := config.Get()
	path := cachePath
	if cfg != nil {
		path = cfg.DataPath(cachePath)
	}

	if dir := filepath.Dir(path); dir != "." {
		os.MkdirAll(dir, 0755)
	}
	bytes, _ := json.MarshalIndent(hc, "", "  ")
	os.WriteFile(path, bytes, 0644)
}

func getBaseUrl() string {
	cfg := config.Get()
	if cfg != nil && cfg.HiveMind.URL != "" {
		return cfg.HiveMind.URL
	}
	return ""
}

func getApiKey() string {
	cfg := config.Get()
	if cfg != nil && cfg.HiveMind.APIKey != "" {
		return cfg.HiveMind.APIKey
	}
	return ""
}

func getPullMode() string {
	cfg := config.Get()
	mode := "auto"
	if cfg != nil && cfg.HiveMind.PullMode != "" {
		mode = cfg.HiveMind.PullMode
	}
	if mode == "manual" {
		return "manual"
	}
	return "auto"
}

func isHiveMindEnabled() bool {
	return getBaseUrl() != "" && getApiKey() != ""
}

func ensureAgentId() string {
	uc := readUserConfig()
	if uc.AgentID != "" {
		return uc.AgentID
	}
	b := make([]byte, 12)
	rand.Read(b)
	agentId := "agt_" + hex.EncodeToString(b)
	uc.AgentID = agentId
	writeUserConfig(uc)
	logger.Log("hivemind", fmt.Sprintf("Generated agentId %s", agentId))
	return agentId
}

func requestJson(pathname string, method string, body interface{}, query map[string]string) (map[string]interface{}, error) {
	if !isHiveMindEnabled() {
		return nil, nil
	}
	u, err := url.Parse(getBaseUrl() + pathname)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	for k, v := range query {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()

	var reqBody io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(b)
	}

	req, err := http.NewRequest(method, u.String(), reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", getApiKey())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := hivemindHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respData, _ := io.ReadAll(resp.Body)
	var payload map[string]interface{}
	json.Unmarshal(respData, &payload)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := fmt.Sprintf("HiveMind %d", resp.StatusCode)
		if e, ok := payload["error"].(string); ok && e != "" {
			errMsg = e
		}
		return nil, fmt.Errorf(errMsg)
	}

	return payload, nil
}

func GetSharedLessonsForPrompt(agentType string, maxLessons int) string {
	role := strings.ToUpper(agentType)
	if role == "" {
		role = "GENERAL"
	}
	hc := readCache()
	var filtered []SharedLesson
	for _, l := range hc.SharedLessons {
		if l.Role == "" || l.Role == role || role == "GENERAL" {
			filtered = append(filtered, l)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Score > filtered[j].Score
	})

	if len(filtered) > maxLessons {
		filtered = filtered[:maxLessons]
	}

	if len(filtered) == 0 {
		return ""
	}

	var lines []string
	for _, l := range filtered {
		scoreStr := ""
		if l.Score != 0 { // Simplification
			scoreStr = fmt.Sprintf(" score=%.1f", l.Score)
		}
		lines = append(lines, fmt.Sprintf("[HIVEMIND%s] %s", scoreStr, l.Rule))
	}

	return strings.Join(lines, "\n")
}

func RegisterHiveMindAgent(reason string) {
	if !isHiveMindEnabled() {
		return
	}
	body := map[string]interface{}{
		"agentId":   ensureAgentId(),
		"version":   agentVersion,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"reason":    reason,
		"capabilities": map[string]interface{}{
			"telegram": os.Getenv("TELEGRAM_BOT_TOKEN") != "",
			"lpagent":  os.Getenv("LPAGENT_API_KEY") != "",
			"dryRun":   os.Getenv("DRY_RUN") == "true",
		},
	}
	_, err := requestJson("/api/hivemind/agents/register", "POST", body, nil)
	if err != nil {
		logger.Log("hivemind_warn", fmt.Sprintf("Agent register failed: %v", err))
	}
}

func PullHiveMindLessons(limit int) {
	if !isHiveMindEnabled() {
		return
	}
	payload, err := requestJson("/api/hivemind/lessons/pull", "GET", nil, map[string]string{
		"agentId": ensureAgentId(),
		"limit":   fmt.Sprintf("%d", limit),
	})
	if err != nil {
		logger.Log("hivemind_warn", fmt.Sprintf("Lesson pull failed: %v", err))
		return
	}

	hc := readCache()
	hc.SharedLessons = []SharedLesson{}
	if lessons, ok := payload["lessons"].([]interface{}); ok {
		for _, raw := range lessons {
			b, _ := json.Marshal(raw)
			var sl SharedLesson
			json.Unmarshal(b, &sl)
			if sl.Rule != "" {
				hc.SharedLessons = append(hc.SharedLessons, sl)
			}
		}
	}
	hc.PulledAt = time.Now().UTC().Format(time.RFC3339)
	writeCache(hc)
}

func PullHiveMindPresets() {
	if !isHiveMindEnabled() {
		return
	}
	payload, err := requestJson("/api/hivemind/presets/pull", "GET", nil, map[string]string{
		"agentId": ensureAgentId(),
	})
	if err != nil {
		logger.Log("hivemind_warn", fmt.Sprintf("Preset pull failed: %v", err))
		return
	}

	hc := readCache()
	if presets, ok := payload["presets"].([]interface{}); ok {
		hc.Presets = presets
	} else {
		hc.Presets = []interface{}{}
	}
	hc.PulledAt = time.Now().UTC().Format(time.RFC3339)
	writeCache(hc)
}

func BootstrapHiveMind() {
	if !isHiveMindEnabled() {
		return
	}
	ensureAgentId()
	RegisterHiveMindAgent("startup")
	if getPullMode() == "auto" {
		PullHiveMindLessons(12)
		PullHiveMindPresets()
	}
}

var heartbeatTicker *time.Ticker

func StartHiveMindBackgroundSync() {
	if !isHiveMindEnabled() || heartbeatTicker != nil {
		return
	}
	heartbeatTicker = time.NewTicker(heartbeatInterval)
	go func() {
		for range heartbeatTicker.C {
			RegisterHiveMindAgent("heartbeat")
			if getPullMode() == "auto" {
				PullHiveMindLessons(12)
				PullHiveMindPresets()
			}
		}
	}()
}

func PushHiveLesson(lesson map[string]interface{}) {
	if !isHiveMindEnabled() {
		return
	}

	body := map[string]interface{}{
		"eventId":   fmt.Sprintf("lesson:%s:%v", ensureAgentId(), lesson["id"]),
		"agentId":   ensureAgentId(),
		"version":   agentVersion,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"lesson":    lesson,
	}

	_, err := requestJson("/api/hivemind/lessons/push", "POST", body, nil)
	if err != nil {
		logger.Log("hivemind_warn", fmt.Sprintf("Lesson push failed: %v", err))
	}
}

func PushHivePerformanceEvent(perf map[string]interface{}) {
	if !isHiveMindEnabled() {
		return
	}

	eventId := perf["eventId"]
	if eventId == nil {
		eventId = fmt.Sprintf("close:%s:%v:%v", ensureAgentId(), perf["pool"], time.Now().Unix())
	}

	closeReason := ""
	if cr, ok := perf["close_reason"].(string); ok {
		closeReason = cr
	}

	countInWinRate := true
	crLower := strings.ToLower(closeReason)
	if strings.Contains(crLower, "out of range") || crLower == "oor" || strings.Contains(crLower, "oor") || strings.Contains(crLower, "pumped far above range") {
		countInWinRate = false
	}

	body := map[string]interface{}{
		"eventId":   eventId,
		"agentId":   ensureAgentId(),
		"version":   agentVersion,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"event": map[string]interface{}{
			"pool":                   perf["pool"],
			"poolName":               perf["pool_name"],
			"baseMint":               perf["base_mint"],
			"strategy":               perf["strategy"],
			"closeReason":            closeReason,
			"pnlUsd":                 perf["pnl_usd"],
			"pnlPct":                 perf["pnl_pct"],
			"feesUsd":                perf["fees_earned_usd"],
			"feesSol":                perf["fees_earned_sol"],
			"minutesHeld":            perf["minutes_held"],
			"countInAdjustedWinRate": countInWinRate,
		},
	}

	_, err := requestJson("/api/hivemind/performance/push", "POST", body, nil)
	if err != nil {
		logger.Log("hivemind_warn", fmt.Sprintf("Performance push failed: %v", err))
	}
}
