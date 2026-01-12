# Building mautrix-go Matrix Bridges

This skill covers building Matrix bridges using the mautrix-go bridgev2 framework and testing them with Beeper's infrastructure.

## Project Structure

A typical mautrix-go bridgev2 bridge follows this structure:

```
bridge-name/
├── cmd/mautrix-{name}/
│   └── main.go              # Entry point
├── pkg/
│   ├── {name}/              # HTTP client for remote service
│   │   ├── client.go        # API client implementation
│   │   └── types.go         # Data types (User, Chat, Message, etc.)
│   └── connector/           # mautrix-go integration
│       ├── connector.go     # NetworkConnector implementation
│       ├── client.go        # NetworkAPI implementation
│       ├── login.go         # Login flow implementation
│       └── sync.go          # Chat/member sync (optional)
├── build.sh                 # Build script for bbctl
├── .gitignore
├── go.mod
└── go.sum
```

## Key Interfaces

### NetworkConnector (connector.go)

The main bridge connector that implements `bridgev2.NetworkConnector`:

```go
type MyConnector struct {
    Bridge *bridgev2.Bridge
}

var _ bridgev2.NetworkConnector = (*MyConnector)(nil)

// Required methods:
func (c *MyConnector) Init(bridge *bridgev2.Bridge)
func (c *MyConnector) Start(ctx context.Context) error
func (c *MyConnector) GetName() bridgev2.BridgeName
func (c *MyConnector) GetDBMetaTypes() database.MetaTypes
func (c *MyConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities
func (c *MyConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader)
func (c *MyConnector) GetBridgeInfoVersion() (info, capabilities int)
func (c *MyConnector) GetLoginFlows() []bridgev2.LoginFlow
func (c *MyConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error)
func (c *MyConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error
```

### NetworkAPI (client.go)

Per-user client that implements `bridgev2.NetworkAPI`:

```go
type MyClient struct {
    UserLogin *bridgev2.UserLogin
    Client    *mypackage.Client  // Your HTTP client
}

var _ bridgev2.NetworkAPI = (*MyClient)(nil)

// Required methods:
func (c *MyClient) Connect(ctx context.Context)           // Note: no error return
func (c *MyClient) Disconnect()
func (c *MyClient) IsLoggedIn() bool
func (c *MyClient) LogoutRemote(ctx context.Context)
func (c *MyClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool
func (c *MyClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error)
func (c *MyClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error)
func (c *MyClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures
func (c *MyClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error)
```

### LoginProcess (login.go)

For cookie-based auth, implement `bridgev2.LoginProcessCookies`:

```go
type CookiesLogin struct {
    User   *bridgev2.User
    Bridge *bridgev2.Bridge
}

var _ bridgev2.LoginProcessCookies = (*CookiesLogin)(nil)

func (l *CookiesLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error)
func (l *CookiesLogin) Cancel()
func (l *CookiesLogin) SubmitCookies(ctx context.Context, cookies map[string]string) (*bridgev2.LoginStep, error)
```

## Common Imports

```go
import (
    "maunium.net/go/mautrix/bridgev2"
    "maunium.net/go/mautrix/bridgev2/database"
    "maunium.net/go/mautrix/bridgev2/networkid"
    "maunium.net/go/mautrix/bridgev2/status"
    "maunium.net/go/mautrix/bridgev2/matrix/mxmain"
    "maunium.net/go/mautrix/event"
    "go.mau.fi/util/configupgrade"
    "github.com/rs/zerolog"
)
```

## Main Entry Point

```go
package main

import (
    "maunium.net/go/mautrix/bridgev2/matrix/mxmain"
    "your-module/pkg/connector"
)

var (
    Tag       = "unknown"
    Commit    = "unknown"
    BuildTime = "unknown"
)

func main() {
    m := mxmain.BridgeMain{
        Name:        "mautrix-mybridge",
        URL:         "https://github.com/org/mybridge",
        Description: "A Matrix-MyService puppeting bridge",
        Version:     "0.1.0",
        Connector:   &connector.MyConnector{},
    }
    m.InitVersion(Tag, Commit, BuildTime)
    m.Run()
}
```

## Room Capabilities

Use `event.RoomFeatures` for capabilities:

```go
func (c *MyClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
    return &event.RoomFeatures{
        MaxTextLength: 10000,
        Reply:         event.CapLevelDropped,      // or CapLevelFullySupported
        Edit:          event.CapLevelDropped,
        Delete:        event.CapLevelDropped,
        Reaction:      event.CapLevelDropped,
        ReadReceipts:  false,
    }
}
```

Capability levels:
- `event.CapLevelRejected` (-2): Feature unsupported, messages rejected
- `event.CapLevelDropped` (-1): Feature unsupported, no fallback
- `event.CapLevelUnsupported` (0): Unsupported but may have fallback
- `event.CapLevelPartialSupport` (1): Partially supported
- `event.CapLevelFullySupported` (2): Fully supported

## User Login Metadata

Store credentials in a metadata struct:

```go
type UserLoginMetadata struct {
    Cookies   string `json:"cookies"`
    CSRFToken string `json:"csrf_token"`
    UserID    int64  `json:"user_id"`
    UserName  string `json:"user_name"`
}
```

Register it in GetDBMetaTypes:

```go
func (c *MyConnector) GetDBMetaTypes() database.MetaTypes {
    return database.MetaTypes{
        UserLogin: func() any {
            return &UserLoginMetadata{}
        },
    }
}
```

## Creating User Logins

```go
loginID := networkid.UserLoginID(fmt.Sprintf("%d", user.ID))
dbUserLogin := &database.UserLogin{
    BridgeID:   bridge.ID,
    UserMXID:   matrixUser.MXID,
    ID:         loginID,
    RemoteName: user.Name,
    RemoteProfile: status.RemoteProfile{
        Name: user.Name,
    },
    Metadata: &UserLoginMetadata{...},
}

ul, err := matrixUser.NewLogin(ctx, dbUserLogin, &bridgev2.NewLoginParams{
    LoadUserLogin: func(ctx context.Context, login *bridgev2.UserLogin) error {
        login.Client = &MyClient{UserLogin: login, Client: httpClient}
        return nil
    },
    DeleteOnConflict: true,
})
```

## Message Backfill

Implement `FetchMessages` for backfill support:

```go
func (c *MyClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
    // Fetch messages from remote service
    messages, hasMore, err := c.Client.GetMessages(ctx, chatID, params.AnchorMessage)

    var backfillMessages []*bridgev2.BackfillMessage
    for _, msg := range messages {
        backfillMessages = append(backfillMessages, &bridgev2.BackfillMessage{
            ConvertedMessage: &bridgev2.ConvertedMessage{
                Parts: []*bridgev2.ConvertedMessagePart{{
                    ID:      networkid.PartID(""),
                    Type:    event.EventMessage,
                    Content: &event.MessageEventContent{
                        MsgType: event.MsgText,
                        Body:    msg.Content,
                    },
                }},
            },
            Sender:    bridgev2.EventSender{Sender: networkid.UserID(msg.SenderID)},
            ID:        networkid.MessageID(msg.ID),
            Timestamp: msg.Timestamp,
        })
    }

    return &bridgev2.FetchMessagesResponse{
        Messages: backfillMessages,
        HasMore:  hasMore,
        Forward:  false,
    }, nil
}
```

## Build Script (build.sh)

Required for `bbctl run --local-dev`:

```bash
#!/bin/bash
cd "$(dirname "$0")"
go build -o mautrix-mybridge ./cmd/mautrix-mybridge
```

## Testing with Beeper (bbctl)

### Login to staging
```bash
bbctl -e staging login
```

### Run bridge in local dev mode
```bash
cd /path/to/bridge
bbctl -e staging run sh-mybridge --type bridgev2 --local-dev
```

### Key flags:
- `--type bridgev2`: Use generic bridgev2 config template
- `--local-dev`: Run from current directory, compile with build.sh
- `--custom-startup-command`: Override startup command
- `-e staging`: Use staging environment

### Bridge naming:
- Self-hosted bridges must start with `sh-` prefix
- Example: `sh-bookface`, `sh-myservice`

## .gitignore Template

```gitignore
# Binaries
/mautrix-*
*.exe
*.dll
*.so
*.dylib

# Test binary
*.test
*.out

# Go workspace
go.work
vendor/

# IDE
.idea/
.vscode/
*.swp

# Config with secrets
config.yaml
*.db
*.db-*

# Logs
*.log
```

## HTTP Client Pattern

For the remote service client:

```go
type Client struct {
    httpClient *http.Client
    baseURL    string
    cookies    string
    csrfToken  string
    log        zerolog.Logger
}

func NewClient(creds *Credentials, log zerolog.Logger) *Client {
    return &Client{
        httpClient: &http.Client{Timeout: 30 * time.Second},
        baseURL:    "https://api.example.com",
        cookies:    creds.Cookies,
        csrfToken:  creds.CSRFToken,
        log:        log.With().Str("component", "api_client").Logger(),
    }
}

func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
    req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
    if err != nil {
        return nil, err
    }

    req.Header.Set("Cookie", c.cookies)
    req.Header.Set("X-CSRF-Token", c.csrfToken)
    req.Header.Set("Accept", "application/json")

    return c.httpClient.Do(req)
}
```

## Reference Bridges

Study these for patterns:
- mautrix-discord
- mautrix-slack
- mautrix-whatsapp
- mautrix-signal
- mautrix-linkedin (cookie-based auth example)
