package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"meridian-go-rewrite/internal/config"
)

var (
	openaiClient *openai.Client
	clientMu     sync.Mutex
	lastAPIKey   string
	lastBaseURL  string
)

func getClient() *openai.Client {
	clientMu.Lock()
	defer clientMu.Unlock()

	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}

	if openaiClient == nil || apiKey != lastAPIKey || baseURL != lastBaseURL {
		cfg := openai.DefaultConfig(apiKey)
		cfg.BaseURL = baseURL
		openaiClient = openai.NewClientWithConfig(cfg)
		lastAPIKey = apiKey
		lastBaseURL = baseURL
	}
	return openaiClient
}

type ToolCallbacks struct {
	OnToolStart  func(name string, args map[string]any)
	OnToolFinish func(name string, result any, success bool)
}

type LoopResult struct {
	Content string
}

type ToolDef struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

func AgentLoop(goal string, maxSteps int, sessionHistory []openai.ChatCompletionMessage, agentType string, model string, maxTokens int, interactive bool, callbacks *ToolCallbacks) (*LoopResult, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, fmt.Errorf("config not loaded")
	}
	if maxSteps <= 0 {
		maxSteps = cfg.LLM.MaxSteps
	}
	if model == "" {
		model = resolveModel(agentType, cfg)
	}
	if model == "" {
		model = os.Getenv("LLM_MODEL")
	}
	if model == "" {
		model = "openrouter/healer-alpha"
	}

	systemPrompt := buildSystemPrompt(agentType, cfg)
	tools := getToolsForRole(agentType)

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
	}
	messages = append(messages, sessionHistory...)
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: goal,
	})

	firedOnce := make(map[string]bool)

	for step := 0; step < maxSteps; step++ {
		var openaiTools []openai.Tool
		if len(tools) > 0 {
			openaiTools = make([]openai.Tool, len(tools))
			for i, t := range tools {
				openaiTools[i] = openai.Tool{
					Type: openai.ToolTypeFunction,
					Function: &openai.FunctionDefinition{
						Name:        t.Function.Name,
						Description: t.Function.Description,
						Parameters:  t.Function.Parameters,
					},
				}
			}
		}

		req := openai.ChatCompletionRequest{
			Model:       model,
			Messages:    messages,
			Temperature: float32(cfg.LLM.Temperature),
			MaxTokens:   cfg.LLM.MaxTokens,
		}
		if len(openaiTools) > 0 {
			req.Tools = openaiTools
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		resp, err := getClient().CreateChatCompletion(ctx, req)
		cancel()
		if err != nil {
			if strings.Contains(err.Error(), "429") {
				time.Sleep(30 * time.Second)
				continue
			}
			return nil, fmt.Errorf("LLM error at step %d: %w", step, err)
		}

		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("no choices in response")
		}

		msg := resp.Choices[0].Message
		messages = append(messages, msg)

		if len(msg.ToolCalls) == 0 {
			if msg.Content == "" {
				messages = messages[:len(messages)-1]
				continue
			}
			// Hallucination Guard for SCREENER
			if agentType == "SCREENER" && strings.Contains(msg.Content, "🚀 DEPLOYED") && !firedOnce["deploy_position"] {
				messages = append(messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: "ERROR: You reported a successful deployment (🚀 DEPLOYED) but did NOT call the 'deploy_position' tool. You must call the 'deploy_position' tool to execute the deployment on-chain. Please call 'deploy_position' now or explain why you cannot.",
				})
				continue
			}
			// Hallucination Guard for MANAGER close actions
			if agentType == "MANAGER" && strings.Contains(msg.Content, "Closed") && !firedOnce["close_position"] {
				messages = append(messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: "ERROR: You reported a successful close (Closed) but did NOT call the 'close_position' tool. You must call the 'close_position' tool to execute the close on-chain. Please call 'close_position' now or explain why you cannot.",
				})
				continue
			}
			return &LoopResult{Content: msg.Content}, nil
		}

		for _, tc := range msg.ToolCalls {
			name := tc.Function.Name
			name = strings.TrimSpace(strings.SplitN(name, "<", 2)[0])

			if firedOnce[name] && (name == "deploy_position" || name == "close_position" || name == "swap_token") {
				messages = append(messages, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					Content:    `{"blocked": true, "reason": "already executed this session"}`,
					ToolCallID: tc.ID,
				})
				continue
			}

			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = map[string]any{}
			}

			if callbacks != nil && callbacks.OnToolStart != nil {
				callbacks.OnToolStart(name, args)
			}

			result := executeTool(name, args, cfg)

			success := true
			if m, ok := result.(map[string]any); ok {
				if errStr, ok := m["error"].(string); ok && errStr != "" {
					success = false
				}
				if blocked, ok := m["blocked"].(bool); ok && blocked {
					success = false
				}
			}

			if callbacks != nil && callbacks.OnToolFinish != nil {
				callbacks.OnToolFinish(name, result, success)
			}

			if name == "deploy_position" || (name == "close_position" && success) || (name == "swap_token" && success) {
				firedOnce[name] = true
			}

			resultJSON, _ := json.Marshal(result)
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    string(resultJSON),
				ToolCallID: tc.ID,
			})
		}
	}

	return &LoopResult{Content: "Max steps reached. Review logs for partial progress."}, nil
}

func resolveModel(agentType string, cfg *config.Config) string {
	switch agentType {
	case "MANAGER":
		return cfg.LLM.ManagementModel
	case "SCREENER":
		return cfg.LLM.ScreeningModel
	default:
		return cfg.LLM.GeneralModel
	}
}
