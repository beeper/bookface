package bookface

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"

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

// cookieJSON is used for JSON serialization (Name/Value only, domain is hardcoded).
type cookieJSON struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

func (s *StringCookieJar) UnmarshalJSON(data []byte) error {
	*s = *NewEmptyStringCookieJar()
	var parsed []cookieJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	cookies := make([]*http.Cookie, len(parsed))
	for i, c := range parsed {
		cookies[i] = &http.Cookie{Name: c.Name, Value: c.Value, Domain: CookieDomain}
	}
	s.jar.SetCookies(CookieBaseURL, cookies)
	return nil
}

func (s *StringCookieJar) MarshalJSON() ([]byte, error) {
	cookies := s.Cookies(CookieBaseURL)
	out := make([]cookieJSON, len(cookies))
	for i, c := range cookies {
		out[i] = cookieJSON{Name: c.Name, Value: c.Value}
	}
	return json.Marshal(out)
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
