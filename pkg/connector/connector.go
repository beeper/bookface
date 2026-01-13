package connector

import (
	"context"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/bookface/pkg/bookface"
)

// BookfaceConnector implements the mautrix bridgev2.NetworkConnector interface.
type BookfaceConnector struct {
	Bridge      *bridgev2.Bridge
	DirectMedia bool
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
	if metadata.Cookies == "" {
		return nil
	}

	client := &BookfaceClient{
		UserLogin: login,
		Client: bookface.NewClient(&bookface.Credentials{
			Cookies:   metadata.Cookies,
			CSRFToken: metadata.CSRFToken,
		}, login.Log),
	}
	login.Client = client
	return nil
}

// UserLoginMetadata stores the credentials for a logged-in user.
type UserLoginMetadata struct {
	Cookies   string `json:"cookies"`
	CSRFToken string `json:"csrf_token"`
	UserID    int64  `json:"user_id"`
	UserName  string `json:"user_name"`
	AvatarURL string `json:"avatar_url,omitempty"`
}
