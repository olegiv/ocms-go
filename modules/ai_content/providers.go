// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package ai_content

import (
	"context"
	"fmt"
)

// ProviderID constants for supported AI providers.
const (
	ProviderOpenAI = "openai"
	ProviderClaude = "claude"
	ProviderGroq   = "groq"
	ProviderOllama = "ollama"
)

// ProviderInfo contains display metadata for a provider.
type ProviderInfo struct {
	ID          string
	Name        string
	Description string
	Models      []ModelInfo
	ImageModels []ModelInfo
	NeedsAPIKey bool
	HasBaseURL  bool
}

// ModelInfo describes an available model.
type ModelInfo struct {
	ID          string
	Name        string
	InputCost   float64 // cost per 1M input tokens in USD
	OutputCost  float64 // cost per 1M output tokens in USD
	ImageCost   float64 // cost per image generation in USD
	ContextSize int     // context window in tokens
}

// ChatMessage represents a message in a chat completion request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest represents a chat completion request.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

// ChatResponse represents a chat completion response.
type ChatResponse struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Model            string
}

// ImageRequest represents an image generation request.
type ImageRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Size    string `json:"size"`
	Quality string `json:"quality,omitempty"`
	N       int    `json:"n"`
}

// ImageResponse represents an image generation response.
type ImageResponse struct {
	ImageData []byte // raw image bytes (PNG)
	Model     string
	CostUSD   float64
}

// Provider is the interface for AI text generation providers.
type Provider interface {
	ID() string
	ChatCompletion(ctx context.Context, apiKey string, req ChatRequest) (*ChatResponse, error)
}

// ImageProvider is the interface for AI image generation providers.
type ImageProvider interface {
	GenerateImage(ctx context.Context, apiKey string, req ImageRequest) (*ImageResponse, error)
}

// AllProviders returns metadata for all supported providers.
func AllProviders() []ProviderInfo {
	return []ProviderInfo{
		{
			ID:          ProviderOpenAI,
			Name:        "OpenAI",
			Description: "GPT models for text generation and DALL-E for image generation",
			NeedsAPIKey: true,
			HasBaseURL:  false,
			Models: []ModelInfo{
				{ID: "gpt-4o", Name: "GPT-4o", InputCost: 2.50, OutputCost: 10.00, ContextSize: 128000},
				{ID: "gpt-4o-mini", Name: "GPT-4o Mini", InputCost: 0.15, OutputCost: 0.60, ContextSize: 128000},
				{ID: "gpt-4.1", Name: "GPT-4.1", InputCost: 2.00, OutputCost: 8.00, ContextSize: 1047576},
				{ID: "gpt-4.1-mini", Name: "GPT-4.1 Mini", InputCost: 0.40, OutputCost: 1.60, ContextSize: 1047576},
				{ID: "gpt-4.1-nano", Name: "GPT-4.1 Nano", InputCost: 0.10, OutputCost: 0.40, ContextSize: 1047576},
				{ID: "o3-mini", Name: "o3-mini", InputCost: 1.10, OutputCost: 4.40, ContextSize: 200000},
			},
			ImageModels: []ModelInfo{
				{ID: "dall-e-3", Name: "DALL-E 3", ImageCost: 0.040},
				{ID: "gpt-image-1", Name: "GPT Image 1", ImageCost: 0.040},
			},
		},
		{
			ID:          ProviderClaude,
			Name:        "Anthropic Claude",
			Description: "Claude models for high-quality text generation",
			NeedsAPIKey: true,
			HasBaseURL:  false,
			Models: []ModelInfo{
				{ID: "claude-sonnet-4-5-20250929", Name: "Claude Sonnet 4.5", InputCost: 3.00, OutputCost: 15.00, ContextSize: 200000},
				{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", InputCost: 0.80, OutputCost: 4.00, ContextSize: 200000},
				{ID: "claude-opus-4-6", Name: "Claude Opus 4.6", InputCost: 15.00, OutputCost: 75.00, ContextSize: 200000},
			},
			ImageModels: nil,
		},
		{
			ID:          ProviderGroq,
			Name:        "Groq",
			Description: "Ultra-fast inference for open-source LLM models",
			NeedsAPIKey: true,
			HasBaseURL:  false,
			Models: []ModelInfo{
				{ID: "llama-3.3-70b-versatile", Name: "Llama 3.3 70B", InputCost: 0.59, OutputCost: 0.79, ContextSize: 128000},
				{ID: "llama-3.1-8b-instant", Name: "Llama 3.1 8B", InputCost: 0.05, OutputCost: 0.08, ContextSize: 131072},
				{ID: "mixtral-8x7b-32768", Name: "Mixtral 8x7B", InputCost: 0.24, OutputCost: 0.24, ContextSize: 32768},
				{ID: "gemma2-9b-it", Name: "Gemma 2 9B", InputCost: 0.20, OutputCost: 0.20, ContextSize: 8192},
			},
			ImageModels: nil,
		},
		{
			ID:          ProviderOllama,
			Name:        "Ollama",
			Description: "Run open-source LLM models locally",
			NeedsAPIKey: false,
			HasBaseURL:  true,
			Models: []ModelInfo{
				{ID: "llama3.3", Name: "Llama 3.3", InputCost: 0, OutputCost: 0, ContextSize: 128000},
				{ID: "llama3.2", Name: "Llama 3.2", InputCost: 0, OutputCost: 0, ContextSize: 128000},
				{ID: "mistral", Name: "Mistral", InputCost: 0, OutputCost: 0, ContextSize: 32768},
				{ID: "gemma2", Name: "Gemma 2", InputCost: 0, OutputCost: 0, ContextSize: 8192},
				{ID: "qwen2.5", Name: "Qwen 2.5", InputCost: 0, OutputCost: 0, ContextSize: 128000},
				{ID: "phi3", Name: "Phi-3", InputCost: 0, OutputCost: 0, ContextSize: 128000},
				{ID: "deepseek-r1", Name: "DeepSeek R1", InputCost: 0, OutputCost: 0, ContextSize: 128000},
			},
			ImageModels: nil,
		},
	}
}

// GetProviderInfo returns provider metadata by ID.
func GetProviderInfo(providerID string) (*ProviderInfo, error) {
	for _, p := range AllProviders() {
		if p.ID == providerID {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("unknown provider: %s", providerID)
}

// GetModelInfo returns model info for a provider and model ID.
func GetModelInfo(providerID, modelID string) (*ModelInfo, error) {
	pInfo, err := GetProviderInfo(providerID)
	if err != nil {
		return nil, err
	}
	for _, m := range pInfo.Models {
		if m.ID == modelID {
			return &m, nil
		}
	}
	return nil, fmt.Errorf("unknown model %s for provider %s", modelID, providerID)
}

// GetImageModelInfo returns image model info for a provider and model ID.
func GetImageModelInfo(providerID, modelID string) (*ModelInfo, error) {
	pInfo, err := GetProviderInfo(providerID)
	if err != nil {
		return nil, err
	}
	for _, m := range pInfo.ImageModels {
		if m.ID == modelID {
			return &m, nil
		}
	}
	return nil, fmt.Errorf("unknown image model %s for provider %s", modelID, providerID)
}

// CalculateCost calculates the cost based on token usage and model pricing.
func CalculateCost(providerID, modelID string, promptTokens, completionTokens int) float64 {
	info, err := GetModelInfo(providerID, modelID)
	if err != nil {
		return 0
	}
	inputCost := float64(promptTokens) / 1_000_000 * info.InputCost
	outputCost := float64(completionTokens) / 1_000_000 * info.OutputCost
	return inputCost + outputCost
}
