package connector

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-bookface/pkg/bookface"
)

// SyncChats fetches all chats and syncs them to Matrix.
func (bc *BookfaceClient) SyncChats(ctx context.Context) error {
	bc.UserLogin.Log.Info().Msg("Starting chat sync")

	chats, err := bc.Client.GetAllChats(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch chats: %w", err)
	}

	bc.UserLogin.Log.Info().Int("count", len(chats)).Msg("Fetched chats from Bookface")

	for _, chat := range chats {
		if err := bc.syncChat(ctx, &chat); err != nil {
			bc.UserLogin.Log.Warn().Err(err).Int64("chat_id", chat.ID).Msg("Failed to sync chat")
			continue
		}
	}

	bc.UserLogin.Log.Info().Msg("Chat sync complete")
	return nil
}

func (bc *BookfaceClient) syncChat(ctx context.Context, chat *bookface.Chat) error {
	portalKey := networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("%d", chat.ID)),
		Receiver: bc.UserLogin.ID,
	}

	chatInfo := bc.chatToInfo(chat)

	// Sync ghost info for all chat users
	for _, cu := range chat.ChatUsers {
		ghostID := networkid.UserID(fmt.Sprintf("%d", cu.UserID))
		ghost, err := bc.UserLogin.Bridge.GetGhostByID(ctx, ghostID)
		if err != nil {
			bc.UserLogin.Log.Warn().Err(err).Int64("user_id", cu.UserID).Msg("Failed to get ghost")
			continue
		}

		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{
			Name: &cu.Name,
		})
	}

	// Create or update the portal
	portal, err := bc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return fmt.Errorf("failed to get portal: %w", err)
	}

	portal.UpdateInfo(ctx, chatInfo, bc.UserLogin, nil, time.Time{})

	bc.UserLogin.Log.Debug().Int64("chat_id", chat.ID).Str("name", chat.Name).Msg("Synced chat")
	return nil
}

// BackfillChat backfills messages for a specific chat.
func (bc *BookfaceClient) BackfillChat(ctx context.Context, chatID int64) error {
	bc.UserLogin.Log.Info().Int64("chat_id", chatID).Msg("Starting message backfill")

	messages, err := bc.Client.GetAllMessages(ctx, chatID)
	if err != nil {
		return fmt.Errorf("failed to fetch messages: %w", err)
	}

	bc.UserLogin.Log.Info().Int64("chat_id", chatID).Int("count", len(messages)).Msg("Fetched messages")

	portalKey := networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("%d", chatID)),
		Receiver: bc.UserLogin.ID,
	}

	portal, err := bc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return fmt.Errorf("failed to get portal: %w", err)
	}

	// Convert and process messages
	var backfillMessages []*bridgev2.BackfillMessage
	for _, msg := range messages {
		// Ensure ghost exists for sender
		if msg.User != nil {
			ghostID := networkid.UserID(fmt.Sprintf("%d", msg.User.ID))
			ghost, err := bc.UserLogin.Bridge.GetGhostByID(ctx, ghostID)
			if err == nil {
				ghost.UpdateInfo(ctx, bc.userToGhostInfo(msg.User))
			}
		}

		converted := bc.convertMessage(&msg)
		if converted != nil {
			backfillMessages = append(backfillMessages, converted)
		}
	}

	if len(backfillMessages) > 0 && portal.MXID != "" {
		bc.UserLogin.Log.Info().Int64("chat_id", chatID).Int("count", len(backfillMessages)).Msg("Backfilling messages to Matrix room")
	}

	return nil
}
