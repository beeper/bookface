package connector

import (
	"context"
	"net/http"
	"sync"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/bookface/pkg/bookface"
)

// BookfaceConnector implements the mautrix bridgev2.NetworkConnector interface.
type BookfaceConnector struct {
	Bridge      *bridgev2.Bridge
	DirectMedia bool

	// cookieJar is shared with user clients for authenticated media fetches.
	// When a user logs in, their client's cookie jar is stored here.
	cookieJar   http.CookieJar
	cookieJarMu sync.RWMutex
}

// SetCookieJar sets the cookie jar used for authenticated requests.
func (bc *BookfaceConnector) SetCookieJar(jar http.CookieJar) {
	bc.cookieJarMu.Lock()
	bc.cookieJar = jar
	bc.cookieJarMu.Unlock()
}

// GetCookieJar returns the cookie jar for authenticated requests.
func (bc *BookfaceConnector) GetCookieJar() http.CookieJar {
	bc.cookieJarMu.RLock()
	defer bc.cookieJarMu.RUnlock()
	return bc.cookieJar
}

var _ bridgev2.NetworkConnector = (*BookfaceConnector)(nil)

func (bc *BookfaceConnector) Init(bridge *bridgev2.Bridge) {
	bc.Bridge = bridge
}

func (bc *BookfaceConnector) Start(ctx context.Context) error {
	return nil
}

func (bc *BookfaceConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:      "Bookface",
		NetworkURL:       "https://messages.ycombinator.com",
		NetworkIcon:      "mxc://maunium.net/placeholder",
		NetworkID:        "bookface",
		BeeperBridgeType: "bookface",
		DefaultPort:      29330,
	}
}

func (bc *BookfaceConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		UserLogin: func() any {
			return &UserLoginMetadata{}
		},
		Message: func() any {
			return &MessageMetadata{}
		},
	}
}

func (bc *BookfaceConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{
		DisappearingMessages: false,
		AggressiveUpdateInfo: false,
	}
}

func (bc *BookfaceConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return "", nil, nil
}

func (bc *BookfaceConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return 1, 1
}

func (bc *BookfaceConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{{
		Name:        "Password",
		Description: "Log in with your YC username and password",
		ID:          "password",
	}}
}

func (bc *BookfaceConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != "password" {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	return &PasswordLogin{
		User:   user,
		Bridge: bc.Bridge,
	}, nil
}

func (bc *BookfaceConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	metadata := login.Metadata.(*UserLoginMetadata)
	if metadata.Cookies == nil {
		return nil
	}

	// Set up callback to persist cookies to database whenever they're updated
	metadata.Cookies.SetOnUpdate(func() {
		err := login.Save(context.Background())
		if err != nil {
			login.Log.Err(err).Msg("Failed to save cookies after update")
		} else {
			login.Log.Debug().Msg("Saved cookies after update")
		}
	})

	bookfaceClient := bookface.NewClient(metadata.Cookies, metadata.CSRFToken, login.Log)

	// Share the client's cookie jar with the connector for authenticated media fetches
	bc.SetCookieJar(bookfaceClient.GetCookieJar())

	client := &BookfaceClient{
		Connector: bc,
		UserLogin: login,
		Client:    bookfaceClient,
	}
	login.Client = client
	return nil
}

// UserLoginMetadata stores the credentials for a logged-in user.
type UserLoginMetadata struct {
	Cookies   *bookface.StringCookieJar `json:"cookies,omitempty"`
	CSRFToken string                    `json:"csrf_token"`
	UserID    int64                     `json:"user_id"`
	UserName  string                    `json:"user_name"`
	AvatarURL string                    `json:"avatar_url,omitempty"`
}

// MessageMetadata stores extra metadata for bridged messages.
type MessageMetadata struct {
	UpdatedAt        int64   `json:"updated_at"`                   // Unix timestamp of message's last update
	ThreadMessageIDs []int64 `json:"thread_message_ids,omitempty"` // IDs of thread replies (only set on thread parent)
}
