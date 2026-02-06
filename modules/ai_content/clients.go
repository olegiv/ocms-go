// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package ai_content

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const httpTimeout = 120 * time.Second

// openAICompatibleClient implements Provider and ImageProvider using the
// official openai-go SDK. It works for OpenAI, Groq, and Ollama via
// option.WithBaseURL().
type openAICompatibleClient struct {
	providerID string
	baseURL    string
}

func newOpenAICompatibleClient(providerID, baseURL string) *openAICompatibleClient {
	return &openAICompatibleClient{
		providerID: providerID,
		baseURL:    baseURL,
	}
}

func (c *openAICompatibleClient) ID() string { return c.providerID }

// buildSDKClient creates an openai.Client configured for this provider.
func (c *openAICompatibleClient) buildSDKClient(apiKey string) openai.Client {
	// Ollama doesn't need auth; SDK requires a non-empty key
	if apiKey == "" {
		apiKey = "not-needed"
	}
	return openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(c.baseURL),
		option.WithHTTPClient(&http.Client{Timeout: httpTimeout}),
	)
}

// ChatCompletion performs a chat completion using the openai-go SDK.
func (c *openAICompatibleClient) ChatCompletion(ctx context.Context, apiKey string, req ChatRequest) (*ChatResponse, error) {
	client := c.buildSDKClient(apiKey)

	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case "user":
			msgs = append(msgs, openai.UserMessage(m.Content))
		case "assistant":
			msgs = append(msgs, openai.AssistantMessage(m.Content))
		default:
			msgs = append(msgs, openai.UserMessage(m.Content))
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: msgs,
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}

	completion, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("%s chat: %w", c.providerID, mapSDKError(err))
	}

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("%s: no choices returned", c.providerID)
	}

	return &ChatResponse{
		Content:          completion.Choices[0].Message.Content,
		PromptTokens:     int(completion.Usage.PromptTokens),
		CompletionTokens: int(completion.Usage.CompletionTokens),
		TotalTokens:      int(completion.Usage.TotalTokens),
		Model:            completion.Model,
	}, nil
}

// GenerateImage generates an image using OpenAI DALL-E or GPT Image.
func (c *openAICompatibleClient) GenerateImage(ctx context.Context, apiKey string, req ImageRequest) (*ImageResponse, error) {
	client := c.buildSDKClient(apiKey)

	params := openai.ImageGenerateParams{
		Prompt: req.Prompt,
		Model:  openai.ImageModel(req.Model),
		N:      openai.Int(1),
		Size:   openai.ImageGenerateParamsSize(req.Size),
	}

	// gpt-image-1 doesn't support response_format; dall-e-3 uses b64_json
	if req.Model == "dall-e-3" {
		params.ResponseFormat = openai.ImageGenerateParamsResponseFormatB64JSON
		if req.Quality != "" {
			params.Quality = openai.ImageGenerateParamsQuality(req.Quality)
		}
	}

	result, err := client.Images.Generate(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai image: %w", mapSDKError(err))
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("openai: no image data returned")
	}

	var imgData []byte
	if result.Data[0].B64JSON != "" {
		imgData, err = base64.StdEncoding.DecodeString(result.Data[0].B64JSON)
		if err != nil {
			return nil, fmt.Errorf("openai image base64 decode: %w", err)
		}
	} else if result.Data[0].URL != "" {
		imgData, err = downloadImage(ctx, result.Data[0].URL)
		if err != nil {
			return nil, fmt.Errorf("openai image download: %w", err)
		}
	} else {
		return nil, fmt.Errorf("openai: no image data in response")
	}

	imgModel, _ := GetImageModelInfo(ProviderOpenAI, req.Model)
	costUSD := 0.0
	if imgModel != nil {
		costUSD = imgModel.ImageCost
	}

	return &ImageResponse{
		ImageData: imgData,
		Model:     req.Model,
		CostUSD:   costUSD,
	}, nil
}

// mapSDKError converts an openai.Error to a descriptive error while
// preserving the original error chain for errors.As callers.
func mapSDKError(err error) error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return fmt.Errorf("api error (status %d): %w", apiErr.StatusCode, err)
	}
	return err
}

// claudeClient implements Provider for Anthropic Claude.
type claudeClient struct {
	baseURL string
}

func newClaudeClient() *claudeClient {
	return &claudeClient{baseURL: "https://api.anthropic.com/v1"}
}

func (c *claudeClient) ID() string { return ProviderClaude }

func (c *claudeClient) ChatCompletion(ctx context.Context, apiKey string, req ChatRequest) (*ChatResponse, error) {
	// Claude API uses a different format
	messages := make([]map[string]string, 0, len(req.Messages))
	systemPrompt := ""
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			systemPrompt = msg.Content
			continue
		}
		messages = append(messages, map[string]string{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}

	body := map[string]any{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": 8192,
	}
	if systemPrompt != "" {
		body["system"] = systemPrompt
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}

	respBody, err := doClaudeRequest(ctx, c.baseURL+"/messages", apiKey, body)
	if err != nil {
		return nil, fmt.Errorf("claude chat: %w", err)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("claude decode: %w", err)
	}

	content := ""
	for _, c := range result.Content {
		if c.Type == "text" {
			content = c.Text
			break
		}
	}

	return &ChatResponse{
		Content:          content,
		PromptTokens:     result.Usage.InputTokens,
		CompletionTokens: result.Usage.OutputTokens,
		TotalTokens:      result.Usage.InputTokens + result.Usage.OutputTokens,
		Model:            result.Model,
	}, nil
}

// doClaudeRequest performs a JSON HTTP request with Anthropic-style auth.
func doClaudeRequest(ctx context.Context, url, apiKey string, body any) ([]byte, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// downloadImage downloads an image from a URL.
func downloadImage(ctx context.Context, imgURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// getProviderClient returns the appropriate Provider for the given settings.
func getProviderClient(settings *ProviderSettings) Provider {
	switch settings.Provider {
	case ProviderOpenAI:
		return newOpenAICompatibleClient(ProviderOpenAI, "https://api.openai.com/v1")
	case ProviderClaude:
		return newClaudeClient()
	case ProviderGroq:
		return newOpenAICompatibleClient(ProviderGroq, "https://api.groq.com/openai/v1")
	case ProviderOllama:
		baseURL := settings.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return newOpenAICompatibleClient(ProviderOllama, baseURL+"/v1")
	default:
		return nil
	}
}
