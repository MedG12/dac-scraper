package scraper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
)

// Login authenticates with the DAC website and returns the ci_session cookie.
func Login() (string, error) {
	baseURL := os.Getenv("DAC_BASE_URL")
	username := os.Getenv("DAC_USERNAME")
	password := os.Getenv("DAC_PASSWORD")

	if baseURL == "" || username == "" || password == "" {
		return "", fmt.Errorf("missing DAC_BASE_URL, DAC_USERNAME, or DAC_PASSWORD in environment")
	}

	loginURL := baseURL + "/auth/ajax_login/"

	// Create multipart body
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("username", username)
	_ = writer.WriteField("password", password)
	err := writer.Close()
	if err != nil {
		return "", fmt.Errorf("failed to create multipart form: %w", err)
	}

	req, err := http.NewRequest("POST", loginURL, body)
	if err != nil {
		return "", fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read raw body for debugging
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse response body to check status
	var result map[string]interface{}
	if err := json.Unmarshal(rawBody, &result); err != nil {
		log.Printf("[Login Debug] Raw HTML Response:\n%s", string(rawBody))
		return "", fmt.Errorf("failed to parse login response: %w", err)
	}

	// Check if login was successful
	statusRaw, ok := result["status"]
	if !ok {
		log.Printf("[Login Debug] Response Missing Status: %s", string(rawBody))
		return "", fmt.Errorf("DAC login failed: no status in response")
	}

	// Status can be string "success" or boolean true
	isSuccess := false
	switch v := statusRaw.(type) {
	case string:
		if v == "success" {
			isSuccess = true
		}
	case bool:
		isSuccess = v
	}

	if !isSuccess {
		msg := "unknown error"
		if m, ok := result["message"].(string); ok {
			msg = m
		}
		log.Printf("[Login Debug] Failed Login Response: %s", string(rawBody))
		return "", fmt.Errorf("%s", msg)
	}

	// Extract ci_session from Set-Cookie header
	var sessionID string
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "ci_session" {
			sessionID = cookie.Value
			break
		}
	}

	if sessionID == "" {
		// Try raw Set-Cookie header
		setCookie := resp.Header.Get("Set-Cookie")
		if setCookie != "" {
			parts := strings.Split(setCookie, ";")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if strings.HasPrefix(p, "ci_session=") {
					sessionID = strings.TrimPrefix(p, "ci_session=")
					break
				}
			}
		}
	}

	if sessionID == "" {
		return "", fmt.Errorf("login succeeded but no ci_session cookie received")
	}

	log.Printf("[Login] Successfully logged in as %s", username)
	return sessionID, nil
}
