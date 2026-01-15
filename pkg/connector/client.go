package connector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/bookface/pkg/bookface"
)

// BookfaceClient implements the mautrix NetworkAPI interface.
type BookfaceClient struct {
	Connector   *BookfaceConnector
	UserLogin   *bridgev2.UserLogin
	Client      *bookface.Client
	actionCable *bookface.ActionCable

	// pendingMetadataUpdates stores metadata that needs to be applied after backfill completes.
	// This is populated when we sync threads for messages that haven't been stored yet,
	// and applied at the start of the next backfill cycle.
	pendingMetadataUpdates map[int64]*MessageMetadata
}

var _ bridgev2.NetworkAPI = (*BookfaceClient)(nil)

func (bc *BookfaceClient) Connect(ctx context.Context) {
	// Validate credentials on connect
	user, err := bc.Client.ValidateAuth(ctx)
	if err != nil {
		bc.UserLogin.Log.Error().Err(err).Msg("Failed to validate auth on connect")
		bc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateBadCredentials,
			RemoteID:   bc.UserLogin.ID,
			Error:      "bookface-auth-failed",
			Message:    err.Error(),
		})
		return
	}
	bc.UserLogin.Log.Info().
		Str("display_name", user.GetDisplayName()).
		Str("hnid", user.HNID).
		Msg("Connected to Bookface")

	// Update own ghost with avatar to get mxc:// URL
	metadata := bc.UserLogin.Metadata.(*UserLoginMetadata)
	ownGhost, err := bc.UserLogin.Bridge.GetGhostByID(ctx, networkid.UserID(fmt.Sprintf("%d", metadata.UserID)))
	if err != nil {
		bc.UserLogin.Log.Warn().Err(err).Msg("Failed to get own ghost")
	} else {
		displayName := user.GetDisplayName()
		ghostInfo := &bridgev2.UserInfo{
			Name:   &displayName,
			Avatar: bc.makeAvatar(ctx, user.GetAvatarURL()),
		}
		ownGhost.UpdateInfo(ctx, ghostInfo)
	}

	// Update and save RemoteProfile
	bc.UserLogin.RemoteName = user.HNID
	bc.UserLogin.RemoteProfile = status.RemoteProfile{
		Name:     user.GetDisplayName(),
		Username: user.HNID,
	}
	if ownGhost != nil {
		bc.UserLogin.RemoteProfile.Avatar = ownGhost.AvatarMXC
	}
	if err := bc.UserLogin.Save(ctx); err != nil {
		bc.UserLogin.Log.Warn().Err(err).Msg("Failed to save user login")
	}

	bc.Client.SetLoggedIn(true)
	bc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnected,
	})

	// Start WebSocket for real-time updates
	if err := bc.StartWebSocket(ctx); err != nil {
		bc.UserLogin.Log.Warn().Err(err).Msg("Failed to start WebSocket, falling back to polling")
	}

	// Sync chats in the background after connecting
	go bc.SyncChats(context.WithoutCancel(ctx))
}

func (bc *BookfaceClient) Disconnect() {
	bc.StopWebSocket()
	if bc.Client != nil {
		bc.Client.SetLoggedIn(false)
	}
	bc.UserLogin.Log.Info().Msg("Disconnected from Bookface")
}

func (bc *BookfaceClient) IsLoggedIn() bool {
	return bc.Client != nil && bc.Client.IsLoggedIn()
}

func (bc *BookfaceClient) LogoutRemote(ctx context.Context) {
	if bc.Client != nil {
		if err := bc.Client.Logout(ctx); err != nil {
			bc.UserLogin.Log.Warn().Err(err).Msg("Failed to logout from remote")
		}
	}
	bc.Disconnect()
	bc.Client = nil
}

func (bc *BookfaceClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	metadata := bc.UserLogin.Metadata.(*UserLoginMetadata)
	return userID == networkid.UserID(fmt.Sprintf("%d", metadata.UserID))
}

func (bc *BookfaceClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{
		MaxTextLength: 10000,
		Reply:         event.CapLevelFullySupported,
		Thread:        event.CapLevelFullySupported,
		Edit:          event.CapLevelFullySupported,
		Delete:        event.CapLevelFullySupported,
		Reaction:      event.CapLevelFullySupported,
		ReadReceipts:  false,
	}
}

func (bc *BookfaceClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	// Parse chat ID
	chatID, err := strconv.ParseInt(string(portal.ID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid chat ID: %w", err)
	}

	// Fetch chats to find this one
	chats, err := bc.Client.GetAllChats(ctx)
	if err != nil {
		return nil, err
	}

	for _, chat := range chats {
		if chat.ID == chatID {
			return bc.chatToInfo(ctx, &chat), nil
		}
	}

	return nil, fmt.Errorf("chat %d not found", chatID)
}

func (bc *BookfaceClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	// User info is extracted from messages/chat users during sync
	return nil, nil
}

// makeEventSender creates an EventSender for a user ID, setting IsFromMe if it's the logged-in user.
func (bc *BookfaceClient) makeEventSender(userID int64) bridgev2.EventSender {
	metadata := bc.UserLogin.Metadata.(*UserLoginMetadata)
	return bridgev2.EventSender{
		IsFromMe: userID == metadata.UserID,
		Sender:   networkid.UserID(fmt.Sprintf("%d", userID)),
	}
}

func (bc *BookfaceClient) chatToInfo(ctx context.Context, chat *bookface.Chat) *bridgev2.ChatInfo {
	chatType := database.RoomTypeDefault
	if chat.IsDM {
		chatType = database.RoomTypeDM
	}

	// Build member map for all valid users (including ourselves)
	memberMap := make(map[networkid.UserID]bridgev2.ChatMember)
	for _, cu := range chat.ChatUsers {
		if cu.User == nil || cu.User.ID == 0 || cu.Deleted {
			continue
		}
		userID := networkid.UserID(fmt.Sprintf("%d", cu.User.ID))
		displayName := cu.User.GetDisplayName()
		memberMap[userID] = bridgev2.ChatMember{
			EventSender: bc.makeEventSender(cu.User.ID),
			Membership:  event.MembershipJoin,
			UserInfo: &bridgev2.UserInfo{
				Name:   &displayName,
				Avatar: bc.makeAvatar(ctx, cu.User.GetAvatarURL()),
			},
		}
	}

	members := &bridgev2.ChatMemberList{
		IsFull:    true,
		MemberMap: memberMap,
	}

	chatInfo := &bridgev2.ChatInfo{
		Type:        &chatType,
		Members:     members,
		CanBackfill: true,
	}
	// Only set name if non-empty; for DMs the bridge will use participant names
	if chat.Name != "" {
		chatInfo.Name = &chat.Name
	}
	return chatInfo
}

func (bc *BookfaceClient) userToGhostInfo(ctx context.Context, user *bookface.User) *bridgev2.UserInfo {
	info := &bridgev2.UserInfo{
		Name:   &user.Name,
		Avatar: bc.makeAvatar(ctx, user.AvatarURL),
	}
	return info
}

// makeAvatar creates a bridgev2.Avatar for the given URL.
// When direct media is enabled, it returns a direct media URI that redirects to the S3 URL.
// Otherwise, it returns a callback that downloads the avatar.
func (bc *BookfaceClient) makeAvatar(ctx context.Context, avatarURL string) *bridgev2.Avatar {
	if avatarURL == "" {
		return &bridgev2.Avatar{Remove: true}
	}

	if bc.Connector.DirectMedia {
		mediaID := MakeAvatarMediaID(avatarURL)
		mxc, err := bc.UserLogin.Bridge.Matrix.GenerateContentURI(ctx, mediaID)
		if err != nil {
			bc.UserLogin.Log.Warn().Err(err).Str("avatar_url", avatarURL).Msg("Failed to generate direct media URI for avatar")
			// Fall back to download method
		} else {
			return &bridgev2.Avatar{
				ID:  networkid.AvatarID(avatarURL),
				MXC: mxc,
			}
		}
	}

	return &bridgev2.Avatar{
		ID: networkid.AvatarID(avatarURL),
		Get: func(ctx context.Context) ([]byte, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, avatarURL, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("User-Agent", bookface.UserAgent)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			return io.ReadAll(resp.Body)
		},
	}
}

