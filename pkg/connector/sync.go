package connector

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/beeper/bookface/pkg/bookface"
)

// SyncChats fetches all chats and syncs them to Matrix.
func (bc *BookfaceClient) SyncChats(ctx context.Context) {
	bc.UserLogin.Log.Info().Msg("Starting chat sync")

	chats, err := bc.Client.GetAllChats(ctx)
	if err != nil {
		bc.UserLogin.Log.Err(err).Msg("Failed to fetch chats")
		return
	}

	bc.UserLogin.Log.Info().Int("count", len(chats)).Msg("Fetched chats from Bookface")

	for _, chat := range chats {
		bc.syncChat(ctx, &chat)
	}

	bc.UserLogin.Log.Info().Msg("Chat sync complete")
}

func (bc *BookfaceClient) syncChat(ctx context.Context, chat *bookface.Chat) {
	portalKey := networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("%d", chat.ID)),
		Receiver: bc.UserLogin.ID,
	}

	// Fetch full member list from /chat_users endpoint
	chatUsersResp, err := bc.Client.GetChatUsers(ctx, chat.ID)
	if err != nil {
		bc.UserLogin.Log.Warn().Err(err).Int64("chat_id", chat.ID).Msg("Failed to fetch chat users, using partial list")
	} else {
		chat.ChatUsers = chatUsersResp.ChatUsers
	}

	chatInfo := bc.chatToInfo(ctx, chat)

	// Get our own user ID to skip ourselves for ghost syncing
	metadata := bc.UserLogin.Metadata.(*UserLoginMetadata)
	ownUserID := metadata.UserID

	// Sync ghost info for all chat users (except ourselves - we're not a ghost)
	bc.UserLogin.Log.Debug().
		Int64("chat_id", chat.ID).
		Int("chat_users_count", len(chat.ChatUsers)).
		Msg("Syncing ghost info for chat users")

	for _, cu := range chat.ChatUsers {
		if cu.User == nil || cu.User.ID == 0 || cu.Deleted {
			continue
		}
		// Skip ourselves - we're not a ghost
		if cu.User.ID == ownUserID {
			continue
		}
		ghostID := networkid.UserID(fmt.Sprintf("%d", cu.User.ID))
		ghost, err := bc.UserLogin.Bridge.GetGhostByID(ctx, ghostID)
		if err != nil {
			bc.UserLogin.Log.Warn().Err(err).Int64("user_id", cu.User.ID).Msg("Failed to get ghost")
			continue
		}
		displayName := cu.User.GetDisplayName()
		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{
			Name:   &displayName,
			Avatar: bc.makeAvatar(ctx, cu.User.GetAvatarURL()),
		})
	}

	// Always trigger forward backfill by using current time as LatestMessageTS.
	// This is necessary because:
	// 1. chat.updated_at only reflects chat metadata changes, not new messages
	// 2. initial_last_message only shows one message, but threads could be on any message
	// 3. We need to fetch recent messages to check each one's updated_at for thread activity
	// The bridge framework will deduplicate messages we already have.
	latestMessageTS := time.Now()

	bc.UserLogin.Log.Info().
		Int64("chat_id", chat.ID).
		Str("name", chat.Name).
		Time("latest_message_ts_for_backfill", latestMessageTS).
		Msg("Chat sync - always triggering forward backfill to check for thread updates")

	// Queue chat resync event - this will create the portal and trigger backfill
	bc.UserLogin.QueueRemoteEvent(&simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventChatResync,
			PortalKey:    portalKey,
			CreatePortal: true,
		},
		ChatInfo:        chatInfo,
		LatestMessageTS: latestMessageTS,
	})

	bc.UserLogin.Log.Debug().
		Int64("chat_id", chat.ID).
		Msg("Queued chat resync event")
}
