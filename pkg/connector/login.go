package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"

	"go.mau.fi/mautrix-bookface/pkg/bookface"
)

// CookiesLogin implements the cookie-based login flow.
type CookiesLogin struct {
	User   *bridgev2.User
	Bridge *bridgev2.Bridge
}

var _ bridgev2.LoginProcessCookies = (*CookiesLogin)(nil)

func (cl *CookiesLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeCookies,
		StepID:       "fi.mau.bookface.cookies",
		Instructions: "Open https://messages.ycombinator.com/messages in your browser, open Developer Tools (F12), go to the Network tab, refresh the page, click on any request to messages.ycombinator.com, and copy the full Cookie header value from the Request Headers section.",
		CookiesParams: &bridgev2.LoginCookiesParams{
			URL:       "https://messages.ycombinator.com/messages",
			UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			Fields: []bridgev2.LoginCookieField{
				{
					ID:       "_messages_session",
					Required: true,
					Sources: []bridgev2.LoginCookieFieldSource{{
						Type:         bridgev2.LoginCookieTypeCookie,
						Name:         "_messages_session",
						CookieDomain: "messages.ycombinator.com",
					}},
				},
				{
					ID:       "XSRF-TOKEN",
					Required: false,
					Sources: []bridgev2.LoginCookieFieldSource{{
						Type:         bridgev2.LoginCookieTypeCookie,
						Name:         "XSRF-TOKEN",
						CookieDomain: "messages.ycombinator.com",
					}},
				},
			},
		},
	}, nil
}

func (cl *CookiesLogin) Cancel() {}

func (cl *CookiesLogin) SubmitCookies(ctx context.Context, cookies map[string]string) (*bridgev2.LoginStep, error) {
	// Build the cookie header string
	var cookieHeader string
	for name, value := range cookies {
		if cookieHeader != "" {
			cookieHeader += "; "
		}
		cookieHeader += fmt.Sprintf("%s=%s", name, value)
	}

	// Create a client to validate the credentials
	creds := &bookface.Credentials{
		Cookies: cookieHeader,
	}
	client := bookface.NewClient(creds, cl.Bridge.Log)

	// Fetch the CSRF token from the page
	csrfToken, err := client.FetchCSRFToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch CSRF token: %w", err)
	}
	creds.CSRFToken = csrfToken
	client.SetCredentials(creds)

	// Validate by fetching user info
	user, err := client.ValidateAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	// Create the database user login entry
	loginID := networkid.UserLoginID(fmt.Sprintf("%d", user.ID))
	dbUserLogin := &database.UserLogin{
		BridgeID:   cl.Bridge.ID,
		UserMXID:   cl.User.MXID,
		ID:         loginID,
		RemoteName: user.Name,
		RemoteProfile: status.RemoteProfile{
			Name: user.Name,
		},
		Metadata: &UserLoginMetadata{
			Cookies:   cookieHeader,
			CSRFToken: csrfToken,
			UserID:    user.ID,
			UserName:  user.Name,
		},
	}

	// Create the user login
	ul, err := cl.User.NewLogin(ctx, dbUserLogin, &bridgev2.NewLoginParams{
		LoadUserLogin: func(ctx context.Context, login *bridgev2.UserLogin) error {
			login.Client = &BookfaceClient{
				UserLogin: login,
				Client:    client,
			}
			return nil
		},
		DeleteOnConflict: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create login: %w", err)
	}

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "fi.mau.bookface.complete",
		Instructions: fmt.Sprintf("Successfully logged in as %s", user.Name),
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: ul.ID,
			UserLogin:   ul,
		},
	}, nil
}
