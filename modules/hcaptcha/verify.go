package hcaptcha

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// hCaptcha verification endpoint
	verifyURL = "https://api.hcaptcha.com/siteverify"
	// Timeout for verification requests
	verifyTimeout = 10 * time.Second
)

// VerifyRequest contains the data needed to verify a captcha response.
type VerifyRequest struct {
	Response  string // h-captcha-response from form
	RemoteIP  string // Client IP address
	Verified  bool   // Set to true after successful verification
	Error     string // Error message if verification failed
	ErrorCode string // Error code for i18n
}

// VerifyResponse represents the hCaptcha API response.
type VerifyResponse struct {
	Success     bool      `json:"success"`
	ChallengeTS time.Time `json:"challenge_ts"`
	Hostname    string    `json:"hostname"`
	ErrorCodes  []string  `json:"error-codes"`
}

// Verify checks the hCaptcha response token with the hCaptcha API.
func (m *Module) Verify(response, remoteIP string) (*VerifyResponse, error) {
	if !m.IsEnabled() {
		// If not enabled, skip verification
		return &VerifyResponse{Success: true}, nil
	}

	if response == "" {
		return nil, fmt.Errorf("missing captcha response")
	}

	// Prepare form data
	data := url.Values{}
	data.Set("secret", m.settings.SecretKey)
	data.Set("response", response)
	if remoteIP != "" {
		data.Set("remoteip", remoteIP)
	}

	// Create HTTP client with timeout
	client := &http.Client{Timeout: verifyTimeout}

	// Make verification request
	resp, err := client.Post(verifyURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("captcha verification request failed: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("nil response from captcha server")
	}
	defer func() { _ = resp.Body.Close() }()

	// Parse response
	var result VerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse captcha response: %w", err)
	}

	return &result, nil
}

// VerifyFromRequest verifies the captcha from a VerifyRequest struct.
func (m *Module) VerifyFromRequest(req *VerifyRequest) (*VerifyRequest, error) {
	if !m.IsEnabled() {
		req.Verified = true
		return req, nil
	}

	if req.Response == "" {
		req.Verified = false
		req.Error = "Please complete the captcha"
		req.ErrorCode = "hcaptcha.error_required"
		m.ctx.Logger.Debug("captcha response empty - user did not complete captcha")
		return req, nil
	}

	result, err := m.Verify(req.Response, req.RemoteIP)
	if err != nil {
		m.ctx.Logger.Error("captcha verification error", "error", err)
		req.Verified = false
		req.Error = "Captcha verification failed"
		req.ErrorCode = "hcaptcha.error_verification"
		return req, nil
	}

	if !result.Success {
		req.Verified = false
		req.Error = "Captcha verification failed"
		req.ErrorCode = "hcaptcha.error_invalid"
		// Log at WARN level so admins can see what went wrong
		m.ctx.Logger.Warn("captcha verification failed",
			"error_codes", result.ErrorCodes,
			"remote_ip", req.RemoteIP,
		)
		return req, nil
	}

	req.Verified = true
	return req, nil
}

// GetResponseFromForm extracts the h-captcha-response from an HTTP request.
func GetResponseFromForm(r *http.Request) string {
	return r.FormValue("h-captcha-response")
}

// GetRemoteIP extracts the client IP from an HTTP request.
func GetRemoteIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for reverse proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the list
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx > 0 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
}
