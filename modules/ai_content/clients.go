// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package ai_content

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const httpTimeout = 120 * time.Second

// openAIClient implements Provider and ImageProvider for OpenAI.
type openAIClient struct {
	baseURL string
}

func newOpenAIClient() *openAIClient {
	return &openAIClient{baseURL: "https://api.openai.com/v1"}
}

func (c *openAIClient) ID() string { return ProviderOpenAI }

func (c *openAIClient) ChatCompletion(ctx context.Context, apiKey string, req ChatRequest) (*ChatResponse, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}

	respBody, err := doJSONRequest(ctx, c.baseURL+"/chat/completions", apiKey, "Bearer", body)
	if err != nil {
		return nil, fmt.Errorf("openai chat: %w", err)
	}

	var result struct {
		Choices []struct {
			Message struct {
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
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("openai decode: %w", err)
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices returned")
	}

	return &ChatResponse{
		Content:          result.Choices[0].Message.Content,
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
		Model:            result.Model,
	}, nil
}

// GenerateImage generates an image using OpenAI DALL-E or GPT Image.
func (c *openAIClient) GenerateImage(ctx context.Context, apiKey string, req ImageRequest) (*ImageResponse, error) {
	body := map[string]any{
		"model":  req.Model,
		"prompt": req.Prompt,
		"n":      1,
		"size":   req.Size,
	}

	// gpt-image-1 doesn't support response_format
	if req.Model == "dall-e-3" {
		body["response_format"] = "b64_json"
		if req.Quality != "" {
			body["quality"] = req.Quality
		}
	}

	respBody, err := doJSONRequest(ctx, c.baseURL+"/images/generations", apiKey, "Bearer", body)
	if err != nil {
		return nil, fmt.Errorf("openai image: %w", err)
	}

	var result struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("openai image decode: %w", err)
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
		// Download image from URL
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

// groqClient implements Provider for Groq.
type groqClient struct {
	baseURL string
}

func newGroqClient() *groqClient {
	return &groqClient{baseURL: "https://api.groq.com/openai/v1"}
}

func (c *groqClient) ID() string { return ProviderGroq }

func (c *groqClient) ChatCompletion(ctx context.Context, apiKey string, req ChatRequest) (*ChatResponse, error) {
	// Groq uses OpenAI-compatible API
	body := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}

	respBody, err := doJSONRequest(ctx, c.baseURL+"/chat/completions", apiKey, "Bearer", body)
	if err != nil {
		return nil, fmt.Errorf("groq chat: %w", err)
	}

	var result struct {
		Choices []struct {
			Message struct {
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
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("groq decode: %w", err)
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("groq: no choices returned")
	}

	return &ChatResponse{
		Content:          result.Choices[0].Message.Content,
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
		Model:            result.Model,
	}, nil
}

// ollamaClient implements Provider for Ollama.
type ollamaClient struct{}

func newOllamaClient() *ollamaClient {
	return &ollamaClient{}
}

func (c *ollamaClient) ID() string { return ProviderOllama }

func (c *ollamaClient) ChatCompletion(ctx context.Context, _ string, req ChatRequest) (*ChatResponse, error) {
	// This should never be called directly; use ollamaChat instead
	return nil, fmt.Errorf("ollama: use ollamaChatWithURL instead")
}

// ollamaChatWithURL performs a chat completion using a custom Ollama base URL.
func ollamaChatWithURL(ctx context.Context, baseURL string, req ChatRequest) (*ChatResponse, error) {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	body := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   false,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ollama marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/chat", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		EvalCount       int    `json:"eval_count"`
		Model           string `json:"model"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("ollama decode: %w", err)
	}

	return &ChatResponse{
		Content:          result.Message.Content,
		PromptTokens:     result.PromptEvalCount,
		CompletionTokens: result.EvalCount,
		TotalTokens:      result.PromptEvalCount + result.EvalCount,
		Model:            result.Model,
	}, nil
}

// doJSONRequest performs a JSON HTTP request with Bearer token auth (OpenAI/Groq compatible).
func doJSONRequest(ctx context.Context, url, apiKey, authScheme string, body any) ([]byte, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authScheme+" "+apiKey)

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

// getProviderClient returns the appropriate Provider implementation for a provider ID.
func getProviderClient(providerID string) Provider {
	switch providerID {
	case ProviderOpenAI:
		return newOpenAIClient()
	case ProviderClaude:
		return newClaudeClient()
	case ProviderGroq:
		return newGroqClient()
	case ProviderOllama:
		return newOllamaClient()
	default:
		return nil
	}
}
