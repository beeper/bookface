package bookface

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	BaseURL         = "https://messages.ycombinator.com"
	BookfaceURL     = "https://bookface.ycombinator.com"
	AccountURL      = "https://account.ycombinator.com"
	DefaultPerPage  = 50
	RequestTimeout  = 30 * time.Second
	UserAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"
)

var csrfMetaRegex = regexp.MustCompile(`<meta\s+name="csrf-token"\s+content="([^"]+)"`)

// Client is an HTTP client for the Bookface API.
type Client struct {
	httpClient *http.Client
	jar        *StringCookieJar
	baseURL    string
	csrfToken  string
	log        zerolog.Logger
	isLoggedIn atomic.Bool
}

// NewClient creates a new Bookface client with the given credentials.
func NewClient(jar *StringCookieJar, csrfToken string, log zerolog.Logger) *Client {
	if jar == nil {
		jar = NewEmptyStringCookieJar()
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: RequestTimeout,
			Jar:     jar,
		},
		jar:       jar,
		baseURL:   BaseURL,
		csrfToken: csrfToken,
		log:       log.With().Str("component", "bookface_client").Logger(),
	}
}

// SetCSRFToken updates the client's CSRF token.
func (c *Client) SetCSRFToken(token string) {
	c.csrfToken = token
}

// GetCookieJar returns the underlying cookie jar.
func (c *Client) GetCookieJar() *StringCookieJar {
	return c.jar
}

// IsLoggedIn returns whether the client is currently logged in.
func (c *Client) IsLoggedIn() bool {
	return c.isLoggedIn.Load()
}

// SetLoggedIn sets the logged in state.
func (c *Client) SetLoggedIn(loggedIn bool) {
	c.isLoggedIn.Store(loggedIn)
}

// setCommonHeaders sets headers common to all requests.
func setCommonHeaders(req *http.Request) {
	req.Header.Set("User-Agent", UserAgent)
}

// setAPIHeaders sets headers for API requests (JSON endpoints).
func (c *Client) setAPIHeaders(req *http.Request) {
	setCommonHeaders(req)
	req.Header.Set("X-CSRF-Token", c.csrfToken)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", c.baseURL+"/messages")
}

// doAPIRequest performs an API request and optionally decodes the JSON response.
// If payload is non-nil, it will be marshaled to JSON and sent as the request body.
// If result is non-nil, the response will be decoded into it.
func (c *Client) doAPIRequest(ctx context.Context, method, path string, payload, result any) error {
	reqURL := c.baseURL + path

	var body io.Reader
	var bodyLen int
	if payload != nil {
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		body = strings.NewReader(string(bodyBytes))
		bodyLen = len(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setAPIHeaders(req)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", c.baseURL)
	}

	c.log.Debug().Str("method", method).Str("url", reqURL).Int("body_length", bodyLen).Msg("Making API request")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("authentication failed (status %d): please re-provision", resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// FetchCSRFToken fetches the CSRF token from the HTML page.
// Session cookies are automatically captured by the cookie jar.
func (c *Client) FetchCSRFToken(ctx context.Context) (string, error) {
	// We need to request HTML explicitly, not JSON
	reqURL := c.baseURL + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	setCommonHeaders(req)
	req.Header.Set("Accept", "text/html")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Set-Cookie headers are automatically handled by the cookie jar

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
	if err := c.doAPIRequest(ctx, http.MethodGet, "/messages/application_shell.json", nil, &resp); err != nil {
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
	if err := c.doAPIRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
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
// Use beforeID to fetch older messages (backward pagination).
// Use afterID to fetch newer messages (forward pagination).
func (c *Client) GetMessages(ctx context.Context, chatID int64, beforeID, afterID *int64) (*MessagesResponse, error) {
	path := fmt.Sprintf("/messages/%d/chat_messages", chatID)
	if beforeID != nil {
		path += "?before_id=" + strconv.FormatInt(*beforeID, 10)
	} else if afterID != nil {
		path += "?after_id=" + strconv.FormatInt(*afterID, 10)
	}

	var resp MessagesResponse
	if err := c.doAPIRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// GetThreadMessages fetches all messages in a thread.
func (c *Client) GetThreadMessages(ctx context.Context, chatID, threadID int64) (*MessagesResponse, error) {
	path := fmt.Sprintf("/messages/%d/chat_messages?thread_id=%d", chatID, threadID)

	var resp MessagesResponse
	if err := c.doAPIRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// GetChatUsers fetches the full member list for a chat.
func (c *Client) GetChatUsers(ctx context.Context, chatID int64) (*ChatUsersResponse, error) {
	path := fmt.Sprintf("/messages/%d/chat_users", chatID)

	var resp ChatUsersResponse
	if err := c.doAPIRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// SendMessage sends a message to a chat.
func (c *Client) SendMessage(ctx context.Context, chatID int64, content, clientMessageID string) (*Message, error) {
	return c.SendMessageWithThread(ctx, chatID, content, clientMessageID, nil)
}

// SendMessageWithThread sends a message to a chat, optionally as a reply in a thread.
func (c *Client) SendMessageWithThread(ctx context.Context, chatID int64, content, clientMessageID string, threadID *int64) (*Message, error) {
	path := fmt.Sprintf("/messages/%d/chat_messages", chatID)

	// Build referrer URL - include thread ID if replying
	referrer := fmt.Sprintf("%s/messages/%d", c.baseURL, chatID)
	if threadID != nil {
		referrer = fmt.Sprintf("%s/messages/%d/%d", c.baseURL, chatID, *threadID)
	}

	req := SendMessageRequest{
		ChatMessage: SendMessagePayload{
			ChatID:          chatID,
			Content:         content,
			MediaUploads:    []any{},
			ClientMessageID: clientMessageID,
			VersionNumber:   2,
			ThreadID:        threadID,
			Referrer:        referrer,
		},
	}

	var msg Message
	if err := c.doAPIRequest(ctx, http.MethodPost, path, req, &msg); err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	c.log.Debug().
		Int64("chat_id", chatID).
		Int64("message_id", msg.ID).
		Str("client_message_id", clientMessageID).
		Interface("thread_id", threadID).
		Msg("Message sent successfully")

	return &msg, nil
}

// EditMessage edits an existing message.
func (c *Client) EditMessage(ctx context.Context, chatID, messageID int64, content, clientMessageID string) (*Message, error) {
	path := fmt.Sprintf("/messages/%d/chat_messages/update", chatID)

	req := EditMessageRequest{
		Content:         content,
		MessageID:       messageID,
		ClientMessageID: clientMessageID,
		ClientID:        uuid.New().String(),
		VersionNumber:   2,
	}

	var msg Message
	if err := c.doAPIRequest(ctx, http.MethodPost, path, req, &msg); err != nil {
		return nil, fmt.Errorf("failed to edit message: %w", err)
	}

	c.log.Debug().
		Int64("chat_id", chatID).
		Int64("message_id", messageID).
		Msg("Message edited successfully")

	return &msg, nil
}

// DeleteMessage deletes a message.
func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	path := fmt.Sprintf("/messages/%d/chat_messages/destroy", chatID)

	req := DeleteMessageRequest{
		MessageID:       messageID,
		ClientMessageID: uuid.New().String(),
	}

	if err := c.doAPIRequest(ctx, http.MethodPost, path, req, nil); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	c.log.Debug().
		Int64("chat_id", chatID).
		Int64("message_id", messageID).
		Msg("Message deleted successfully")

	return nil
}

// SendReaction adds a reaction to a message.
func (c *Client) SendReaction(ctx context.Context, userID, messageID int64, emojiID string) (*SendReactionResponse, error) {
	req := SendReactionRequest{
		EmojiID:          emojiID,
		EmojiVariant:     nil,
		UserID:           userID,
		ReactionableID:   messageID,
		ReactionableType: "ChatMessage",
		ClientID:         uuid.New().String(),
	}

	var resp SendReactionResponse
	if err := c.doAPIRequest(ctx, http.MethodPost, "/reactions", req, &resp); err != nil {
		return nil, fmt.Errorf("failed to send reaction: %w", err)
	}

	c.log.Debug().
		Int64("message_id", messageID).
		Str("emoji_id", emojiID).
		Msg("Reaction sent successfully")

	return &resp, nil
}

// RemoveReaction removes a reaction from a message.
func (c *Client) RemoveReaction(ctx context.Context, userID, messageID int64, emojiID string) error {
	req := SendReactionRequest{
		EmojiID:          emojiID,
		EmojiVariant:     nil,
		UserID:           userID,
		ReactionableID:   messageID,
		ReactionableType: "ChatMessage",
		ClientID:         uuid.New().String(),
	}

	if err := c.doAPIRequest(ctx, http.MethodPost, "/reactions/delete", req, nil); err != nil {
		return fmt.Errorf("failed to remove reaction: %w", err)
	}

	c.log.Debug().
		Int64("message_id", messageID).
		Str("emoji_id", emojiID).
		Msg("Reaction removed successfully")

	return nil
}

// Logout invalidates the current session by calling the logout endpoint.
// The flow is: bookface/session/logout -> account.ycombinator.com/sign_out
func (c *Client) Logout(ctx context.Context) error {
	// Use the existing cookie jar - it already has all our session cookies
	// The httpClient will follow redirects automatically
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/session/logout", nil)
	if err != nil {
		return fmt.Errorf("failed to create logout request: %w", err)
	}

	setCommonHeaders(req)
	req.Header.Set("Accept", "text/html")

	c.log.Debug().Msg("Calling logout endpoint")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("logout request failed: %w", err)
	}
	defer resp.Body.Close()

	c.log.Debug().
		Int("status_code", resp.StatusCode).
		Str("final_url", resp.Request.URL.String()).
		Msg("Logout response received")

	return nil
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

// Login authenticates with YC SSO and returns a cookie jar with the _sso.key cookie.
func Login(ctx context.Context, ycid, password string, log zerolog.Logger) (*StringCookieJar, error) {
	log = log.With().Str("component", "yc_login").Logger()

	// Create a StringCookieJar to capture cookies (uses publicsuffix for domain matching)
	jar := NewEmptyStringCookieJar()
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
	setCommonHeaders(req)
	req.Header.Set("Accept", "text/html")

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
	setCommonHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", csrfToken)

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

	// Step 3: Verify _sso.key cookie is in the jar
	// The cookie is set on .ycombinator.com domain and the jar handles domain matching
	ssoKey := jar.GetCookie("_sso.key")
	if ssoKey == "" {
		// Log available cookies for debugging
		var cookieNames []string
		for _, c := range jar.Cookies(CookieBaseURL) {
			cookieNames = append(cookieNames, c.Name)
		}
		log.Error().Strs("available_cookies", cookieNames).Msg("_sso.key cookie not found in response")
		return nil, fmt.Errorf("login succeeded but _sso.key cookie not found")
	}

	log.Info().Msg("Successfully logged in to YC SSO")
	return jar, nil
}

