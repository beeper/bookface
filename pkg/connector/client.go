package connector

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/bookface/pkg/bookface"
)

// BookfaceClient implements the mautrix NetworkAPI interface.
type BookfaceClient struct {
	UserLogin *bridgev2.UserLogin
	Client    *bookface.Client
}

var _ bridgev2.NetworkAPI = (*BookfaceClient)(nil)

func (bc *BookfaceClient) Connect(ctx context.Context) {
	// Validate credentials on connect
	user, err := bc.Client.ValidateAuth(ctx)
	if err != nil {
		bc.UserLogin.Log.Error().Err(err).Msg("Failed to validate auth on connect")
		bc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateBadCredentials,
			Error:      "bookface-auth-failed",
			Message:    err.Error(),
		})
		return
	}
	bc.UserLogin.Log.Info().
		Str("display_name", user.GetDisplayName()).
		Str("hnid", user.HNID).
		Msg("Connected to Bookface")
	bc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnected,
		RemoteProfile: &status.RemoteProfile{
			Name:     user.GetDisplayName(),
			Username: user.HNID,
		},
	})
}

func (bc *BookfaceClient) Disconnect() {
	bc.UserLogin.Log.Info().Msg("Disconnected from Bookface")
}

func (bc *BookfaceClient) IsLoggedIn() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := bc.Client.ValidateAuth(ctx)
	return err == nil
}

func (bc *BookfaceClient) LogoutRemote(ctx context.Context) {
	// No remote logout needed for cookie-based auth
}

func (bc *BookfaceClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	metadata := bc.UserLogin.Metadata.(*UserLoginMetadata)
	return userID == networkid.UserID(fmt.Sprintf("%d", metadata.UserID))
}

func (bc *BookfaceClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{
		MaxTextLength: 10000,
		Reply:         event.CapLevelDropped,
		Edit:          event.CapLevelDropped,
		Delete:        event.CapLevelDropped,
		Reaction:      event.CapLevelDropped,
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
			return bc.chatToInfo(&chat), nil
		}
	}

	return nil, fmt.Errorf("chat %d not found", chatID)
}

func (bc *BookfaceClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	// User info is extracted from messages/chat users during sync
	return nil, nil
}

func (bc *BookfaceClient) chatToInfo(chat *bookface.Chat) *bridgev2.ChatInfo {
	chatType := database.RoomTypeDefault
	if chat.IsDM {
		chatType = database.RoomTypeDM
	}

	members := &bridgev2.ChatMemberList{
		IsFull: true,
	}

	for _, cu := range chat.ChatUsers {
		members.Members = append(members.Members, bridgev2.ChatMember{
			EventSender: bridgev2.EventSender{
				Sender: networkid.UserID(fmt.Sprintf("%d", cu.UserID)),
			},
			Membership: event.MembershipJoin,
		})
	}

	return &bridgev2.ChatInfo{
		Name:        &chat.Name,
		Type:        &chatType,
		Members:     members,
		CanBackfill: true,
	}
}

func (bc *BookfaceClient) userToGhostInfo(user *bookface.User) *bridgev2.UserInfo {
	info := &bridgev2.UserInfo{
		Name: &user.Name,
	}
	if user.AvatarURL != "" {
		info.Avatar = &bridgev2.Avatar{
			ID: networkid.AvatarID(user.AvatarURL),
			Get: func(ctx context.Context) ([]byte, error) {
				// TODO: Implement avatar fetching
				return nil, nil
			},
		}
	}
	return info
}

// FetchMessages implements backfill support.
func (bc *BookfaceClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	chatID, err := strconv.ParseInt(string(params.Portal.ID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid chat ID: %w", err)
	}

	var before *int64
	if params.AnchorMessage != nil {
		msgID, err := strconv.ParseInt(string(params.AnchorMessage.ID), 10, 64)
		if err == nil {
			before = &msgID
		}
	}

	resp, err := bc.Client.GetMessages(ctx, chatID, before)
	if err != nil {
		return nil, err
	}

	var messages []*bridgev2.BackfillMessage
	for _, msg := range resp.Messages {
		converted := bc.convertMessage(&msg)
		if converted != nil {
			messages = append(messages, converted)
		}
	}

	return &bridgev2.FetchMessagesResponse{
		Messages: messages,
		HasMore:  resp.HasMoreBefore,
		Forward:  false,
	}, nil
}

func (bc *BookfaceClient) convertMessage(msg *bookface.Message) *bridgev2.BackfillMessage {
	sender := bridgev2.EventSender{
		Sender: networkid.UserID(fmt.Sprintf("%d", msg.UserID)),
	}

	content := &event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          msg.Content,
		FormattedBody: msg.HTMLContent,
	}
	if msg.HTMLContent != "" {
		content.Format = event.FormatHTML
	}

	parts := []*bridgev2.ConvertedMessagePart{{
		ID:      networkid.PartID(""),
		Type:    event.EventMessage,
		Content: content,
	}}

	// Handle media uploads - store URL in Extra for later processing
	for _, media := range msg.MediaUploads {
		mediaPart := &bridgev2.ConvertedMessagePart{
			ID:   networkid.PartID(fmt.Sprintf("media_%d", media.ID)),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType: event.MsgFile,
				Body:    media.Filename,
			},
			Extra: map[string]any{
				"bookface_media_url": media.URL,
			},
		}
		parts = append(parts, mediaPart)
	}

	return &bridgev2.BackfillMessage{
		ConvertedMessage: &bridgev2.ConvertedMessage{
			Parts: parts,
		},
		Sender:    sender,
		ID:        networkid.MessageID(fmt.Sprintf("%d", msg.ID)),
		Timestamp: msg.CreatedAt,
	}
}

// HandleMatrixMessage handles outgoing messages from Matrix.
func (bc *BookfaceClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	// TODO: Implement sending messages to Bookface
	return nil, fmt.Errorf("sending messages is not yet implemented")
}
