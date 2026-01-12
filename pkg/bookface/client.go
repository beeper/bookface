package bookface

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

const (
	BaseURL         = "https://messages.ycombinator.com"
	AccountURL      = "https://account.ycombinator.com"
	DefaultPerPage  = 50
	RequestTimeout  = 30 * time.Second
)

var csrfMetaRegex = regexp.MustCompile(`<meta\s+name="csrf-token"\s+content="([^"]+)"`)

// Client is an HTTP client for the Bookface API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	cookies    string
	csrfToken  string
	log        zerolog.Logger
}

// NewClient creates a new Bookface client with the given credentials.
func NewClient(creds *Credentials, log zerolog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: RequestTimeout,
		},
		baseURL:   BaseURL,
		cookies:   creds.Cookies,
		csrfToken: creds.CSRFToken,
		log:       log.With().Str("component", "bookface_client").Logger(),
	}
}

// SetCredentials updates the client's credentials.
func (c *Client) SetCredentials(creds *Credentials) {
	c.cookies = creds.Cookies
	c.csrfToken = creds.CSRFToken
}

// doRequest performs an HTTP request with the appropriate headers.
func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	reqURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Cookie", c.cookies)
	req.Header.Set("X-CSRF-Token", c.csrfToken)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", c.baseURL+"/messages")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	c.log.Debug().Str("method", method).Str("url", reqURL).Int("cookie_length", len(c.cookies)).Msg("Making request")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// doJSONRequest performs a request and decodes the JSON response.
func (c *Client) doJSONRequest(ctx context.Context, method, path string, body io.Reader, result interface{}) error {
	resp, err := c.doRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("authentication failed (status %d): please re-provision", resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// FetchCSRFToken fetches the CSRF token from the HTML page.
func (c *Client) FetchCSRFToken(ctx context.Context) (string, error) {
	// We need to request HTML explicitly, not JSON
	reqURL := c.baseURL + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Cookie", c.cookies)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	c.log.Debug().Int("status_code", resp.StatusCode).Int("body_length", len(bodyBytes)).Msg("CSRF token fetch response")

	if resp.StatusCode != http.StatusOK {
		c.log.Error().Int("status_code", resp.StatusCode).Str("body", string(bodyBytes[:min(500, len(bodyBytes))])).Msg("Failed to fetch CSRF token page")
		return "", fmt.Errorf("failed to fetch page: status %d", resp.StatusCode)
	}

	matches := csrfMetaRegex.FindSubmatch(bodyBytes)
	if len(matches) < 2 {
		c.log.Error().Str("body_preview", string(bodyBytes[:min(500, len(bodyBytes))])).Msg("CSRF token not found in page")
		return "", fmt.Errorf("CSRF token not found in page")
	}

	return string(matches[1]), nil
}

// ValidateAuth validates the current credentials by fetching application shell.
func (c *Client) ValidateAuth(ctx context.Context) (*User, error) {
	var resp ApplicationShellResponse
	if err := c.doJSONRequest(ctx, http.MethodGet, "/messages/application_shell.json", nil, &resp); err != nil {
		return nil, err
	}

	if resp.CurrentUser == nil {
		return nil, fmt.Errorf("not authenticated: no current user in response")
	}

	return resp.CurrentUser, nil
}

// GetChats fetches the chat list with optional pagination.
func (c *Client) GetChats(ctx context.Context, cursor *string, perPage int) (*ChatsResponse, error) {
	if perPage <= 0 {
		perPage = DefaultPerPage
	}

	path := fmt.Sprintf("/messages.json?per_page=%d", perPage)
	if cursor != nil && *cursor != "" {
		path += "&cursor=" + url.QueryEscape(*cursor)
	}

	var resp ChatsResponse
	if err := c.doJSONRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// GetAllChats fetches all chats by paginating through the entire list.
func (c *Client) GetAllChats(ctx context.Context) ([]Chat, error) {
	var allChats []Chat
	var cursor *string

	for {
		resp, err := c.GetChats(ctx, cursor, DefaultPerPage)
		if err != nil {
			return nil, err
		}

		allChats = append(allChats, resp.Chats...)
		c.log.Debug().Int("fetched", len(resp.Chats)).Int("total", len(allChats)).Int("remaining", resp.RemainingCount).Msg("Fetched chats page")

		if resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor

		// Small delay to avoid rate limiting
		time.Sleep(100 * time.Millisecond)
	}

	return allChats, nil
}

// GetMessages fetches messages for a chat with optional pagination.
func (c *Client) GetMessages(ctx context.Context, chatID int64, before *int64) (*MessagesResponse, error) {
	path := fmt.Sprintf("/messages/%d/chat_messages", chatID)
	if before != nil {
		path += "?before=" + strconv.FormatInt(*before, 10)
	}

	var resp MessagesResponse
	if err := c.doJSONRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// GetAllMessages fetches all messages for a chat by paginating backwards.
func (c *Client) GetAllMessages(ctx context.Context, chatID int64) ([]Message, error) {
	var allMessages []Message
	var before *int64

	for {
		resp, err := c.GetMessages(ctx, chatID, before)
		if err != nil {
			return nil, err
		}

		allMessages = append(allMessages, resp.Messages...)
		c.log.Debug().Int64("chat_id", chatID).Int("fetched", len(resp.Messages)).Int("total", len(allMessages)).Bool("has_more", resp.HasMoreBefore).Msg("Fetched messages page")

		if !resp.HasMoreBefore || len(resp.Messages) == 0 {
			break
		}

		// Find the oldest message ID for the next request
		oldestID := resp.Messages[0].ID
		for _, msg := range resp.Messages {
			if msg.ID < oldestID {
				oldestID = msg.ID
			}
		}
		before = &oldestID

		// Small delay to avoid rate limiting
		time.Sleep(100 * time.Millisecond)
	}

	return allMessages, nil
}

// LoginRequest represents the request body for YC SSO login.
type LoginRequest struct {
	YCID     string  `json:"ycid"`
	Password string  `json:"password"`
	Captcha  *string `json:"captcha"`
	TOTP     string  `json:"totp"`
	Continue string  `json:"continue"`
}

// LoginResponse represents the response from YC SSO login.
type LoginResponse struct {
	Success bool   `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Login authenticates with YC SSO and returns credentials with the _sso.key cookie.
func Login(ctx context.Context, ycid, password string, log zerolog.Logger) (*Credentials, error) {
	log = log.With().Str("component", "yc_login").Logger()

	// Create a client with cookie jar to capture cookies
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}
	httpClient := &http.Client{
		Timeout: RequestTimeout,
		Jar:     jar,
	}

	// Step 1: Fetch the account page to get CSRF token
	log.Debug().Msg("Fetching account page for CSRF token")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, AccountURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create login page request: %w", err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch login page: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read login page: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Error().Int("status_code", resp.StatusCode).Str("body", string(bodyBytes[:min(500, len(bodyBytes))])).Msg("Failed to fetch login page")
		return nil, fmt.Errorf("failed to fetch login page: status %d", resp.StatusCode)
	}

	// Extract CSRF token from meta tag
	matches := csrfMetaRegex.FindSubmatch(bodyBytes)
	if len(matches) < 2 {
		log.Error().Str("body_preview", string(bodyBytes[:min(500, len(bodyBytes))])).Msg("CSRF token not found in login page")
		return nil, fmt.Errorf("CSRF token not found in login page")
	}
	csrfToken := string(matches[1])
	log.Debug().Msg("Got CSRF token from login page")

	// Step 2: POST credentials to sign_in endpoint
	loginReq := LoginRequest{
		YCID:     ycid,
		Password: password,
		Captcha:  nil,
		TOTP:     "",
		Continue: "https://bookface.ycombinator.com/",
	}
	loginBody, err := json.Marshal(loginReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal login request: %w", err)
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodPost, AccountURL+"/sign_in", strings.NewReader(string(loginBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create sign_in request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	log.Debug().Str("ycid", ycid).Msg("Submitting login credentials")
	resp, err = httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Debug().Int("status_code", resp.StatusCode).Int("body_length", len(respBody)).Msg("Login response received")

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		var loginResp LoginResponse
		if json.Unmarshal(respBody, &loginResp) == nil && loginResp.Error != "" {
			return nil, fmt.Errorf("authentication failed: %s", loginResp.Error)
		}
		return nil, fmt.Errorf("authentication failed: invalid credentials")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	// Step 3: Extract _sso.key cookie from the cookie jar
	// The cookie is set on .ycombinator.com domain
	ycURL, _ := url.Parse("https://ycombinator.com")
	var ssoKey string
	for _, cookie := range jar.Cookies(ycURL) {
		if cookie.Name == "_sso.key" {
			ssoKey = cookie.Value
			break
		}
	}

	if ssoKey == "" {
		// Also check account.ycombinator.com
		accountURL, _ := url.Parse(AccountURL)
		for _, cookie := range jar.Cookies(accountURL) {
			if cookie.Name == "_sso.key" {
				ssoKey = cookie.Value
				break
			}
		}
	}

	if ssoKey == "" {
		log.Error().Msg("_sso.key cookie not found in response")
		return nil, fmt.Errorf("login succeeded but _sso.key cookie not found")
	}

	log.Info().Msg("Successfully logged in to YC SSO")
	return &Credentials{
		Cookies: fmt.Sprintf("_sso.key=%s", ssoKey),
	}, nil
}

// ParseCookies parses a cookie header string and extracts individual cookies.
func ParseCookies(cookieHeader string) map[string]string {
	cookies := make(map[string]string)
	parts := strings.Split(cookieHeader, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if idx := strings.Index(part, "="); idx > 0 {
			key := strings.TrimSpace(part[:idx])
			value := strings.TrimSpace(part[idx+1:])
			cookies[key] = value
		}
	}
	return cookies
}
