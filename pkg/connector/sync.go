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

	chatInfo := bc.chatToInfo(chat)

	// Get our own user ID to skip ourselves
	metadata := bc.UserLogin.Metadata.(*UserLoginMetadata)
	ownUserID := metadata.UserID

	// Sync ghost info for all chat users (except ourselves - we're not a ghost)
	bc.UserLogin.Log.Debug().
		Int64("chat_id", chat.ID).
		Int("chat_users_count", len(chat.ChatUsers)).
		Msg("Syncing ghost info for chat users")

	for _, cu := range chat.ChatUsers {
		if cu.User == nil || cu.User.ID == 0 || cu.Deleted {
			bc.UserLogin.Log.Debug().
				Bool("user_nil", cu.User == nil).
				Bool("deleted", cu.Deleted).
				Msg("Skipping invalid/deleted chat user")
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
		bc.UserLogin.Log.Debug().
			Int64("user_id", cu.User.ID).
			Str("display_name", displayName).
			Str("avatar_url", cu.User.GetAvatarURL()).
			Msg("Updating ghost info")
		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{
			Name:   &displayName,
			Avatar: makeAvatar(cu.User.GetAvatarURL()),
		})
	}

	// Get latest message timestamp for backfill check
	var latestMessageTS time.Time
	if chat.InitialLastMessage != nil {
		latestMessageTS = chat.InitialLastMessage.CreatedAt
	}

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
		Str("name", chat.Name).
		Bool("has_initial_last_message", chat.InitialLastMessage != nil).
		Time("latest_message_ts", latestMessageTS).
		Msg("Queued chat sync")
}
