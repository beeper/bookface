package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/beeper/bookface/pkg/bookface"
)

// PasswordLogin implements the username/password login flow for YC SSO.
type PasswordLogin struct {
	User   *bridgev2.User
	Bridge *bridgev2.Bridge
}

var _ bridgev2.LoginProcessUserInput = (*PasswordLogin)(nil)

func (pl *PasswordLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       "fi.mau.bookface.password",
		Instructions: "Enter your YC (Y Combinator) login credentials. These are the same credentials you use to log into Bookface or other YC services.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:        bridgev2.LoginInputFieldTypeUsername,
					ID:          "ycid",
					Name:        "YC ID",
					Description: "Your YC username (e.g., your Bookface login)",
				},
				{
					Type:        bridgev2.LoginInputFieldTypePassword,
					ID:          "password",
					Name:        "Password",
					Description: "Your YC account password",
				},
			},
		},
	}, nil
}

func (pl *PasswordLogin) Cancel() {}

func (pl *PasswordLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	ycid := input["ycid"]
	password := input["password"]

	if ycid == "" || password == "" {
		return nil, fmt.Errorf("username and password are required")
	}

	pl.Bridge.Log.Debug().Str("ycid", ycid).Msg("Attempting YC SSO login")

	// Authenticate with YC SSO
	creds, err := bookface.Login(ctx, ycid, password, pl.Bridge.Log)
	if err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}

	// Create a client with the credentials
	client := bookface.NewClient(creds, pl.Bridge.Log)

	// Fetch the CSRF token from the messages page
	csrfToken, err := client.FetchCSRFToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch CSRF token: %w", err)
	}
	creds.CSRFToken = csrfToken
	client.SetCredentials(creds)

	// Validate by fetching user info
	user, err := client.ValidateAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("authentication validation failed: %w", err)
	}

	displayName := user.GetDisplayName()
	avatarURL := user.GetAvatarURL()

	pl.Bridge.Log.Debug().
		Int64("user_id", user.ID).
		Str("hnid", user.HNID).
		Str("display_name", displayName).
		Str("avatar_url", avatarURL).
		Msg("Got user info from Bookface")

	// Create the database user login entry
	loginID := networkid.UserLoginID(fmt.Sprintf("%d", user.ID))
	dbUserLogin := &database.UserLogin{
		BridgeID:   pl.Bridge.ID,
		UserMXID:   pl.User.MXID,
		ID:         loginID,
		RemoteName: displayName,
		RemoteProfile: status.RemoteProfile{
			Name:     displayName,
			Username: user.HNID,
		},
		Metadata: &UserLoginMetadata{
			Cookies:   creds.Cookies,
			CSRFToken: csrfToken,
			UserID:    user.ID,
			UserName:  displayName,
			AvatarURL: avatarURL,
		},
	}

	// Create the user login
	ul, err := pl.User.NewLogin(ctx, dbUserLogin, &bridgev2.NewLoginParams{
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
		Instructions: fmt.Sprintf("Successfully logged in as %s", displayName),
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: ul.ID,
			UserLogin:   ul,
		},
	}, nil
}
