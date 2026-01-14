package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/beeper/bookface/pkg/bookface"
)

// handleActionCableEvent processes real-time events from ActionCable.
func (bc *BookfaceClient) handleActionCableEvent(evt *bookface.ActionCableEvent) {
	log := bc.UserLogin.Log.With().
		Int64("chat_id", evt.ChatID).
		Str("event_type", evt.Type).
		Logger()

	switch evt.Type {
	case "chat_message_updated":
		bc.handleMessageUpdated(evt.ChatID, evt.Message)

	case "chat_message_deleted":
		bc.handleMessageDeleted(evt.ChatID, evt.Message)

	case "user_last_active_on_chat_at":
		log.Debug().
			Int64("user_id", evt.UserID).
			Time("at", evt.LastActiveOnChat).
			Msg("User activity update")

	default:
		log.Debug().Msg("Unhandled event type")
	}
}

// handleMessageUpdated processes a real-time message update.
func (bc *BookfaceClient) handleMessageUpdated(chatID int64, msg *bookface.Message) {
	if msg == nil {
		bc.UserLogin.Log.Warn().Int64("chat_id", chatID).Msg("Received nil message in update")
		return
	}

	log := bc.UserLogin.Log.With().
		Int64("chat_id", chatID).
		Int64("message_id", msg.ID).
		Logger()

	log.Debug().Msg("Processing real-time message")

	portalKey := networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("%d", chatID)),
		Receiver: bc.UserLogin.ID,
	}

	// Update ghost info for sender if available
	if msg.User != nil {
		ctx := context.Background()
		ghostID := networkid.UserID(fmt.Sprintf("%d", msg.User.ID))
		ghost, err := bc.UserLogin.Bridge.GetGhostByID(ctx, ghostID)
		if err == nil && ghost != nil {
			displayName := msg.User.GetDisplayName()
			ghost.UpdateInfo(ctx, &bridgev2.UserInfo{
				Name:   &displayName,
				Avatar: bc.makeAvatar(ctx, msg.User.GetAvatarURL()),
			})
		}
	}

	// Queue the message event using the shared convertToMatrix function
	bc.UserLogin.QueueRemoteEvent(&simplevent.Message[*bookface.Message]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessage,
			PortalKey: portalKey,
			Sender:    bc.makeEventSender(msg.UserID),
			Timestamp: msg.CreatedAt,
		},
		ID:                 networkid.MessageID(fmt.Sprintf("%d", msg.ID)),
		Data:               msg,
		ConvertMessageFunc: bc.convertToMatrix,
	})

	// Sync reactions using ReactionSync to handle both additions and removals.
	// Bookface sends the full list of reactions on each message update, so we can
	// use ReactionSync with HasAllUsers=true to let the bridge framework compute
	// which reactions need to be added or removed.
	messageID := networkid.MessageID(fmt.Sprintf("%d", msg.ID))
	reactionData := bc.buildReactionSyncData(msg.Reactions)
	bc.UserLogin.QueueRemoteEvent(&simplevent.ReactionSync{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventReactionSync,
			PortalKey: portalKey,
		},
		TargetMessage: messageID,
		Reactions:     reactionData,
	})

	log.Debug().Int("reaction_count", len(msg.Reactions)).Msg("Queued real-time message and reaction sync events")
}

// handleMessageDeleted processes a real-time message deletion.
func (bc *BookfaceClient) handleMessageDeleted(chatID int64, msg *bookface.Message) {
	if msg == nil {
		bc.UserLogin.Log.Warn().Int64("chat_id", chatID).Msg("Received nil message in delete event")
		return
	}

	log := bc.UserLogin.Log.With().
		Int64("chat_id", chatID).
		Int64("message_id", msg.ID).
		Logger()

	log.Debug().Msg("Processing real-time message deletion")

	portalKey := networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("%d", chatID)),
		Receiver: bc.UserLogin.ID,
	}

	// Queue the message removal event
	bc.UserLogin.QueueRemoteEvent(&simplevent.MessageRemove{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessageRemove,
			PortalKey: portalKey,
			Sender:    bc.makeEventSender(msg.UserID),
		},
		TargetMessage: networkid.MessageID(fmt.Sprintf("%d", msg.ID)),
	})

	log.Debug().Msg("Queued real-time message deletion event")
}

// buildReactionSyncData converts Bookface reactions to a ReactionSyncData structure.
// This groups reactions by user and marks that we have all users and all reactions,
// allowing the bridge framework to properly sync (add new, remove missing) reactions.
func (bc *BookfaceClient) buildReactionSyncData(reactions []bookface.Reaction) *bridgev2.ReactionSyncData {
	data := &bridgev2.ReactionSyncData{
		Users:       make(map[networkid.UserID]*bridgev2.ReactionSyncUser),
		HasAllUsers: true, // Bookface sends all reactions on each update
	}

	for _, r := range reactions {
		if r.User == nil {
			continue
		}
		userID := networkid.UserID(fmt.Sprintf("%d", r.User.ID))

		user, ok := data.Users[userID]
		if !ok {
			user = &bridgev2.ReactionSyncUser{
				HasAllReactions: true, // We have all reactions from this user for this message
			}
			data.Users[userID] = user
		}

		user.Reactions = append(user.Reactions, &bridgev2.BackfillReaction{
			Sender:    bc.makeEventSender(r.User.ID),
			EmojiID:   networkid.EmojiID(r.EmojiID),
			Emoji:     shortcodeToEmoji(r.EmojiID),
			Timestamp: r.CreatedAt,
		})
	}

	return data
}
