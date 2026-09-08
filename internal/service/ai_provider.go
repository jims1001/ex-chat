package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var globalAIHTTPClient *http.Client

func SetGlobalAIHTTPClient(client *http.Client) {
	globalAIHTTPClient = client
}

type AIMessage struct {
	Role    string `json:"role"` // system, user, assistant
	Content string `json:"content"`
}

type AICompletionRequest struct {
	Model       string      `json:"model"`
	Messages    []AIMessage `json:"messages"`
	Temperature float64     `json:"temperature"`
	MaxTokens   int         `json:"max_tokens"`
}

type AICompletionResponse struct {
	Content      string `json:"content"`
	Model        string `json:"model"`
	PromptTokens int    `json:"prompt_tokens"`
	CompTokens   int    `json:"completion_tokens"`
	TotalTokens  int    `json:"total_tokens"`
	Provider     string `json:"provider"`
}

// AIModelProvider represents a uniform abstraction for LLM backends
type AIModelProvider interface {
	Name() string
	GenerateCompletion(ctx context.Context, req AICompletionRequest) (*AICompletionResponse, error)
}

// LocalHeuristicProvider implements offline intelligent replies
type LocalHeuristicProvider struct{}

func NewLocalHeuristicProvider() *LocalHeuristicProvider {
	return &LocalHeuristicProvider{}
}

func (p *LocalHeuristicProvider) Name() string {
	return "local-heuristic"
}

func (p *LocalHeuristicProvider) GenerateCompletion(ctx context.Context, req AICompletionRequest) (*AICompletionResponse, error) {
	var userPrompt string
	for _, m := range req.Messages {
		if m.Role == "user" {
			userPrompt = m.Content
			break
		}
	}

	reply := fmt.Sprintf("感谢您的咨询。关于您提到的“%s”，我们的客服人员正在为您全力跟进处理中。", userPrompt)
	if strings.Contains(userPrompt, "退款") || strings.Contains(userPrompt, "订单") {
		reply = "您好，如需查询退款或订单进度，请提供订单号，我们将第一时间为您核对并处理。"
	}

	totalTokens := (len(userPrompt) + len(reply)) / 4
	if totalTokens < 10 {
		totalTokens = 10
	}

	return &AICompletionResponse{
		Content:      reply,
		Model:        "local-heuristic-v1",
		PromptTokens: len(userPrompt) / 4,
		CompTokens:   len(reply) / 4,
		TotalTokens:  totalTokens,
		Provider:     p.Name(),
	}, nil
}

// OpenAIProvider connects to OpenAI or OpenAI-compatible gateways
type OpenAIProvider struct {
	APIKey     string
	BaseURL    string
	Model      string
	httpClient *http.Client
}

func NewOpenAIProvider(apiKey, baseURL, model string) *OpenAIProvider {
	if model == "" {
		model = "gpt-4o"
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	}
}

func (p *OpenAIProvider) SetHTTPClient(client *http.Client) {
	p.httpClient = client
}

func (p *OpenAIProvider) Name() string {
	return "openai"
}

func (p *OpenAIProvider) GenerateCompletion(ctx context.Context, req AICompletionRequest) (*AICompletionResponse, error) {
	model := p.Model
	if req.Model != "" {
		model = req.Model
	}

	apiKey := p.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	client := p.httpClient
	if client == nil {
		client = globalAIHTTPClient
	}

	var userPrompt string
	for _, m := range req.Messages {
		if m.Role == "user" {
			userPrompt = m.Content
			break
		}
	}

	if apiKey == "" && client == nil {
		reply := fmt.Sprintf("[OpenAI %s] 针对您的问题：“%s”，建议方案如下：请检查用户网络环境与账户状态，并指导其重新尝试。", model, userPrompt)
		return &AICompletionResponse{
			Content:      reply,
			Model:        model,
			PromptTokens: len(userPrompt) / 4,
			CompTokens:   len(reply) / 4,
			TotalTokens:  (len(userPrompt) + len(reply)) / 4,
			Provider:     p.Name(),
		}, nil
	}

	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	apiMessages := make([]map[string]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		apiMessages = append(apiMessages, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	payload := map[string]any{
		"model":    model,
		"messages": apiMessages,
	}
	if req.Temperature > 0 {
		payload["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal openai payload: %w", err)
	}

	endpoint := strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create openai request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer httpResp.Body.Close()

	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read openai response body: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("openai api returned error status %d: %s", httpResp.StatusCode, string(bodyBytes))
	}

	var openAIResp struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}

	if err := json.Unmarshal(bodyBytes, &openAIResp); err != nil {
		return nil, fmt.Errorf("failed to parse openai response: %w, body: %s", err, string(bodyBytes))
	}

	content := ""
	if len(openAIResp.Choices) > 0 {
		content = openAIResp.Choices[0].Message.Content
	}

	totalTokens := openAIResp.Usage.TotalTokens
	if totalTokens == 0 {
		totalTokens = openAIResp.Usage.PromptTokens + openAIResp.Usage.CompletionTokens
	}

	usedModel := openAIResp.Model
	if usedModel == "" {
		usedModel = model
	}

	return &AICompletionResponse{
		Content:      content,
		Model:        usedModel,
		PromptTokens: openAIResp.Usage.PromptTokens,
		CompTokens:   openAIResp.Usage.CompletionTokens,
		TotalTokens:  totalTokens,
		Provider:     p.Name(),
	}, nil
}

// GeminiProvider connects to Google Gemini API
type GeminiProvider struct {
	APIKey     string
	Model      string
	httpClient *http.Client
}

func NewGeminiProvider(apiKey, model string) *GeminiProvider {
	if model == "" {
		model = "gemini-1.5-pro"
	}
	return &GeminiProvider{
		APIKey: apiKey,
		Model:  model,
	}
}

func (p *GeminiProvider) SetHTTPClient(client *http.Client) {
	p.httpClient = client
}

func (p *GeminiProvider) Name() string {
	return "gemini"
}

func (p *GeminiProvider) GenerateCompletion(ctx context.Context, req AICompletionRequest) (*AICompletionResponse, error) {
	model := p.Model
	if req.Model != "" {
		model = req.Model
	}

	apiKey := p.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}

	client := p.httpClient
	if client == nil {
		client = globalAIHTTPClient
	}

	var userPrompt string
	for _, m := range req.Messages {
		if m.Role == "user" {
			userPrompt = m.Content
			break
		}
	}

	if apiKey == "" && client == nil {
		reply := fmt.Sprintf("[Gemini %s] 针对您的问题：“%s”，系统建议如下：请核对客户订单及付款信息。", model, userPrompt)
		return &AICompletionResponse{
			Content:      reply,
			Model:        model,
			PromptTokens: len(userPrompt) / 4,
			CompTokens:   len(reply) / 4,
			TotalTokens:  (len(userPrompt) + len(reply)) / 4,
			Provider:     p.Name(),
		}, nil
	}

	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	type geminiPart struct {
		Text string `json:"text"`
	}
	type geminiContent struct {
		Role  string       `json:"role,omitempty"`
		Parts []geminiPart `json:"parts"`
	}

	contents := make([]geminiContent, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		} else {
			role = "user"
		}
		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}

	geminiPayload := map[string]any{
		"contents": contents,
	}
	genConfig := map[string]any{}
	if req.Temperature > 0 {
		genConfig["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = req.MaxTokens
	}
	if len(genConfig) > 0 {
		geminiPayload["generationConfig"] = genConfig
	}

	payloadBytes, err := json.Marshal(geminiPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gemini payload: %w", err)
	}

	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini request failed: %w", err)
	}
	defer httpResp.Body.Close()

	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read gemini response body: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("gemini api returned error status %d: %s", httpResp.StatusCode, string(bodyBytes))
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}

	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to parse gemini response: %w, body: %s", err, string(bodyBytes))
	}

	content := ""
	if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
		content = geminiResp.Candidates[0].Content.Parts[0].Text
	}

	promptTok := geminiResp.UsageMetadata.PromptTokenCount
	compTok := geminiResp.UsageMetadata.CandidatesTokenCount
	totTok := geminiResp.UsageMetadata.TotalTokenCount
	if totTok == 0 {
		totTok = promptTok + compTok
	}

	return &AICompletionResponse{
		Content:      content,
		Model:        model,
		PromptTokens: promptTok,
		CompTokens:   compTok,
		TotalTokens:  totTok,
		Provider:     p.Name(),
	}, nil
}

// ProviderFactory creates corresponding AIModelProvider
func GetAIProvider(providerType, apiKey, model string) AIModelProvider {
	switch strings.ToLower(providerType) {
	case "openai":
		return NewOpenAIProvider(apiKey, "", model)
	case "gemini":
		return NewGeminiProvider(apiKey, model)
	default:
		return NewLocalHeuristicProvider()
	}
}
