// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package ai_content

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GenerateInput contains the user's input for content generation.
type GenerateInput struct {
	Topic          string
	TargetAudience string
	Tone           string
	KeyPoints      string
	LanguageCode   string
	LanguageName   string
	ContentType    string // "post" or "page"
	AdditionalInfo string
}

// GeneratedContent contains the AI-generated content.
type GeneratedContent struct {
	Title           string `json:"title"`
	Body            string `json:"body"`
	MetaKeywords    string `json:"meta_keywords"`
	MetaDescription string `json:"meta_description"`
	MetaTitle       string `json:"meta_title"`
	Slug            string `json:"slug"`
	ImagePrompt     string `json:"image_prompt"`
}

// buildSystemPrompt creates the system prompt for content generation.
func buildSystemPrompt(langName string) string {
	return fmt.Sprintf(`You are an expert content writer and SEO specialist. You write content in %s language.

You must respond with a valid JSON object (no markdown code fences, no extra text) with exactly these fields:

{
  "title": "An engaging, SEO-optimized title",
  "body": "Full article content in HTML format using <h2>, <h3>, <p>, <ul>, <ol>, <li>, <strong>, <em>, <blockquote> tags. Minimum 500 words. Well-structured with multiple sections.",
  "meta_keywords": "Comma-separated relevant keywords (5-10 keywords)",
  "meta_description": "Compelling meta description under 160 characters",
  "meta_title": "SEO-optimized meta title under 60 characters",
  "slug": "url-friendly-slug-in-english",
  "image_prompt": "A detailed prompt for generating a featured image that visually represents the article content. Describe the scene, style, colors, and mood. Should be suitable for a blog header image."
}

Important rules:
- Write ALL content (title, body, meta fields) in %s
- The slug must be in English (lowercase, hyphens, no special characters)
- HTML body should be well-formatted with proper paragraphs and headings
- Do not use <h1> tags (the title is displayed as h1 separately)
- Make the content informative, engaging, and original
- Include a clear introduction and conclusion
- Respond ONLY with the JSON object, no other text`, langName, langName)
}

// buildUserPrompt creates the user prompt for content generation.
func buildUserPrompt(input *GenerateInput) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Write an article about: %s\n\n", input.Topic))

	if input.TargetAudience != "" {
		sb.WriteString(fmt.Sprintf("Target audience: %s\n", input.TargetAudience))
	}
	if input.Tone != "" {
		sb.WriteString(fmt.Sprintf("Tone: %s\n", input.Tone))
	}
	if input.KeyPoints != "" {
		sb.WriteString(fmt.Sprintf("Key points to cover:\n%s\n", input.KeyPoints))
	}
	if input.ContentType != "" {
		sb.WriteString(fmt.Sprintf("Content type: %s\n", input.ContentType))
	}
	if input.AdditionalInfo != "" {
		sb.WriteString(fmt.Sprintf("Additional instructions: %s\n", input.AdditionalInfo))
	}

	return sb.String()
}

// generateContent generates the article content using the configured AI provider.
func (m *Module) generateContent(ctx context.Context, settings *ProviderSettings, input *GenerateInput) (*GeneratedContent, *ChatResponse, error) {
	systemPrompt := buildSystemPrompt(input.LanguageName)
	userPrompt := buildUserPrompt(input)

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	req := ChatRequest{
		Model:       settings.Model,
		Messages:    messages,
		MaxTokens:   8192,
		Temperature: 0.7,
	}

	var chatResp *ChatResponse
	var err error

	if settings.Provider == ProviderOllama {
		chatResp, err = ollamaChatWithURL(ctx, settings.BaseURL, req)
	} else {
		client := getProviderClient(settings.Provider)
		if client == nil {
			return nil, nil, fmt.Errorf("unsupported provider: %s", settings.Provider)
		}
		chatResp, err = client.ChatCompletion(ctx, settings.APIKey, req)
	}

	if err != nil {
		return nil, nil, fmt.Errorf("AI generation failed: %w", err)
	}

	// Parse the JSON response
	content, err := parseGeneratedContent(chatResp.Content)
	if err != nil {
		return nil, chatResp, fmt.Errorf("failed to parse AI response: %w", err)
	}

	return content, chatResp, nil
}

// parseGeneratedContent extracts the JSON from the AI response.
func parseGeneratedContent(response string) (*GeneratedContent, error) {
	// Try direct JSON parse first
	content := &GeneratedContent{}
	cleaned := strings.TrimSpace(response)

	// Remove markdown code fences if present
	if strings.HasPrefix(cleaned, "```json") {
		cleaned = strings.TrimPrefix(cleaned, "```json")
		cleaned = strings.TrimSuffix(cleaned, "```")
		cleaned = strings.TrimSpace(cleaned)
	} else if strings.HasPrefix(cleaned, "```") {
		cleaned = strings.TrimPrefix(cleaned, "```")
		cleaned = strings.TrimSuffix(cleaned, "```")
		cleaned = strings.TrimSpace(cleaned)
	}

	if err := json.Unmarshal([]byte(cleaned), content); err != nil {
		// Try to find JSON within the response
		start := strings.Index(response, "{")
		end := strings.LastIndex(response, "}")
		if start >= 0 && end > start {
			jsonStr := response[start : end+1]
			if err2 := json.Unmarshal([]byte(jsonStr), content); err2 != nil {
				return nil, fmt.Errorf("could not parse JSON from response: %w (original: %w)", err2, err)
			}
		} else {
			return nil, fmt.Errorf("no JSON found in response: %w", err)
		}
	}

	if content.Title == "" || content.Body == "" {
		return nil, fmt.Errorf("incomplete content: title and body are required")
	}

	return content, nil
}

// generateFeaturedImage generates a featured image using OpenAI DALL-E.
func (m *Module) generateFeaturedImage(ctx context.Context, prompt string) (*ImageResponse, error) {
	// Image generation always uses OpenAI
	openaiSettings, err := loadProviderSettings(m.ctx.DB, ProviderOpenAI)
	if err != nil {
		return nil, fmt.Errorf("loading OpenAI settings for image: %w", err)
	}
	if openaiSettings.APIKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required for image generation")
	}
	if !openaiSettings.ImageEnabled {
		return nil, fmt.Errorf("image generation is not enabled in OpenAI settings")
	}

	imageModel := openaiSettings.ImageModel
	if imageModel == "" {
		imageModel = "dall-e-3"
	}

	client := newOpenAIClient()
	imgReq := ImageRequest{
		Model:   imageModel,
		Prompt:  prompt,
		Size:    "1792x1024",
		Quality: "standard",
		N:       1,
	}

	return client.GenerateImage(ctx, openaiSettings.APIKey, imgReq)
}
