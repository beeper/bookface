package bookface

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// CookieBaseURL is the base URL used for cookie storage.
// The publicsuffix list handles domain matching for subdomains.
var CookieBaseURL, _ = url.Parse(BaseURL)

// CookieDomain is the domain set on cookies for cross-subdomain access.
// This allows cookies to work across messages.ycombinator.com and bookface.ycombinator.com.
const CookieDomain = ".ycombinator.com"

// StringCookieJar is an http.CookieJar implementation that can be
// serialized to/from JSON for storage in the database.
// It supports an optional callback that fires whenever cookies are updated.
type StringCookieJar struct {
	jar      *cookiejar.Jar
	onUpdate func()
}

var (
	_ http.CookieJar   = (*StringCookieJar)(nil)
	_ json.Marshaler   = (*StringCookieJar)(nil)
	_ json.Unmarshaler = (*StringCookieJar)(nil)
)

// NewEmptyStringCookieJar creates an empty StringCookieJar.
func NewEmptyStringCookieJar() *StringCookieJar {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &StringCookieJar{jar: jar}
}

// SetCookies implements http.CookieJar and calls onUpdate if set.
func (s *StringCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	s.jar.SetCookies(u, cookies)
	if s.onUpdate != nil {
		s.onUpdate()
	}
}

// Cookies implements http.CookieJar.
func (s *StringCookieJar) Cookies(u *url.URL) []*http.Cookie {
	return s.jar.Cookies(u)
}

// SetOnUpdate sets a callback that fires whenever cookies are updated.
func (s *StringCookieJar) SetOnUpdate(callback func()) {
	s.onUpdate = callback
}

// NewJarFromCookieHeader creates a StringCookieJar from a cookie header string.
func NewJarFromCookieHeader(cookieHeader string) (*StringCookieJar, error) {
	jar := NewEmptyStringCookieJar()
	return jar, jar.parseCookieHeaderString(cookieHeader)
}

func (s *StringCookieJar) UnmarshalJSON(data []byte) error {
	*s = *NewEmptyStringCookieJar()
	if len(data) > 0 && data[0] == '"' {
		// Legacy format: plain string
		var cookieHeader string
		if err := json.Unmarshal(data, &cookieHeader); err != nil {
			return err
		}
		return s.parseCookieHeaderString(cookieHeader)
	}
	// New format: array of cookies
	var cookies []*http.Cookie
	if err := json.Unmarshal(data, &cookies); err != nil {
		return err
	}
	// Use jar directly to avoid triggering onUpdate when loading from database
	s.jar.SetCookies(CookieBaseURL, cookies)
	return nil
}

func (s *StringCookieJar) MarshalJSON() ([]byte, error) {
	cookies := s.Cookies(CookieBaseURL)
	// Ensure all cookies have domain set for cross-subdomain access
	// (jar.Cookies() doesn't populate Domain field, but we need it for proper restoration)
	for _, c := range cookies {
		if c.Domain == "" {
			c.Domain = CookieDomain
		}
	}
	return json.Marshal(cookies)
}

// GetCookie returns the value of a cookie by name.
func (s *StringCookieJar) GetCookie(name string) string {
	for _, cookie := range s.Cookies(CookieBaseURL) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

// Clear removes all cookies from the jar while preserving the onUpdate callback.
func (s *StringCookieJar) Clear() {
	s.jar, _ = cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
}

// CookieHeader returns the cookies as a header string for HTTP requests.
func (s *StringCookieJar) CookieHeader() string {
	cookies := s.Cookies(CookieBaseURL)
	if len(cookies) == 0 {
		return ""
	}
	var parts []string
	for _, c := range cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// parseCookieHeaderString parses a cookie header and adds cookies to the jar.
// This uses jar directly to avoid triggering onUpdate (used during deserialization).
func (s *StringCookieJar) parseCookieHeaderString(cookieString string) error {
	if cookieString == "" {
		return nil
	}
	cookies, err := http.ParseCookie(cookieString)
	if err != nil {
		return err
	}
	s.jar.SetCookies(CookieBaseURL, cookies)
	return nil
}
