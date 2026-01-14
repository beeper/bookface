package connector

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/enescakir/emoji"
	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

var _ bridgev2.EditHandlingNetworkAPI = (*BookfaceClient)(nil)
var _ bridgev2.RedactionHandlingNetworkAPI = (*BookfaceClient)(nil)
var _ bridgev2.ReactionHandlingNetworkAPI = (*BookfaceClient)(nil)

// HandleMatrixMessage handles outgoing messages from Matrix to Bookface.
func (bc *BookfaceClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	// Extract text content from the message
	content := msg.Content.Body
	if content == "" {
		return nil, fmt.Errorf("empty message content")
	}

	// Parse the portal ID to get the chat ID
	chatID, err := strconv.ParseInt(string(msg.Portal.ID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid portal ID: %w", err)
	}

	// Check if this is a reply/thread message
	var threadID *int64
	if msg.ThreadRoot != nil {
		// Use thread root as the thread_id
		threadMsgID, err := strconv.ParseInt(string(msg.ThreadRoot.ID), 10, 64)
		if err == nil {
			threadID = &threadMsgID
		}
	} else if msg.ReplyTo != nil {
		// For simple replies without thread context, use reply target as thread_id
		replyMsgID, err := strconv.ParseInt(string(msg.ReplyTo.ID), 10, 64)
		if err == nil {
			threadID = &replyMsgID
		}
	}

	// Generate a unique client message ID
	clientMessageID := uuid.New().String()

	bc.UserLogin.Log.Debug().
		Int64("chat_id", chatID).
		Str("client_message_id", clientMessageID).
		Int("content_length", len(content)).
		Interface("thread_id", threadID).
		Msg("Sending message to Bookface")

	// Send the message with optional thread_id
	sentMsg, err := bc.Client.SendMessageWithThread(ctx, chatID, content, clientMessageID, threadID)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	bc.UserLogin.Log.Info().
		Int64("chat_id", chatID).
		Int64("message_id", sentMsg.ID).
		Msg("Message sent successfully")

	// Build the response with thread info if applicable
	dbMsg := &database.Message{
		ID:        networkid.MessageID(fmt.Sprintf("%d", sentMsg.ID)),
		SenderID:  networkid.UserID(fmt.Sprintf("%d", sentMsg.User.ID)),
		Timestamp: sentMsg.CreatedAt,
	}
	if threadID != nil {
		threadRoot := networkid.MessageID(fmt.Sprintf("%d", *threadID))
		dbMsg.ThreadRoot = threadRoot
	}

	return &bridgev2.MatrixMessageResponse{
		DB: dbMsg,
	}, nil
}

// HandleMatrixEdit handles edit events from Matrix to Bookface.
func (bc *BookfaceClient) HandleMatrixEdit(ctx context.Context, msg *bridgev2.MatrixEdit) error {
	// Extract the new content
	newContent := msg.Content.Body
	if newContent == "" {
		return fmt.Errorf("empty edit content")
	}

	// Parse the portal ID to get the chat ID
	chatID, err := strconv.ParseInt(string(msg.Portal.ID), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid portal ID: %w", err)
	}

	// Parse the message ID from the edit target
	messageID, err := strconv.ParseInt(string(msg.EditTarget.ID), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}

	// Generate a client message ID for the edit
	// Note: Ideally we'd use the original client_message_id, but we don't store it
	clientMessageID := uuid.New().String()

	bc.UserLogin.Log.Debug().
		Int64("chat_id", chatID).
		Int64("message_id", messageID).
		Int("content_length", len(newContent)).
		Msg("Editing message on Bookface")

	// Edit the message
	_, err = bc.Client.EditMessage(ctx, chatID, messageID, newContent, clientMessageID)
	if err != nil {
		return fmt.Errorf("failed to edit message: %w", err)
	}

	bc.UserLogin.Log.Info().
		Int64("chat_id", chatID).
		Int64("message_id", messageID).
		Msg("Message edited successfully")

	return nil
}

// HandleMatrixMessageRemove handles message deletion events from Matrix to Bookface.
func (bc *BookfaceClient) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	// Parse the portal ID to get the chat ID
	chatID, err := strconv.ParseInt(string(msg.Portal.ID), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid portal ID: %w", err)
	}

	// Parse the message ID from the target message
	messageID, err := strconv.ParseInt(string(msg.TargetMessage.ID), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}

	bc.UserLogin.Log.Debug().
		Int64("chat_id", chatID).
		Int64("message_id", messageID).
		Msg("Deleting message on Bookface")

	// Delete the message
	if err := bc.Client.DeleteMessage(ctx, chatID, messageID); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	bc.UserLogin.Log.Info().
		Int64("chat_id", chatID).
		Int64("message_id", messageID).
		Msg("Message deleted successfully")

	return nil
}

// PreHandleMatrixReaction prepares reaction handling by returning emoji ID and sender info.
func (bc *BookfaceClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	metadata := bc.UserLogin.Metadata.(*UserLoginMetadata)
	emojiStr := msg.Content.RelatesTo.Key

	// Convert unicode emoji to shortcode for Bookface
	shortcode := emojiToShortcode(emojiStr)

	return bridgev2.MatrixReactionPreResponse{
		SenderID:     networkid.UserID(fmt.Sprintf("%d", metadata.UserID)),
		EmojiID:      networkid.EmojiID(shortcode),
		Emoji:        emojiStr,
		MaxReactions: 0, // No limit on reactions per user
	}, nil
}

// HandleMatrixReaction sends a reaction to Bookface.
func (bc *BookfaceClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	metadata := bc.UserLogin.Metadata.(*UserLoginMetadata)

	// Parse the message ID
	messageID, err := strconv.ParseInt(string(msg.TargetMessage.ID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid message ID: %w", err)
	}

	// Get emoji shortcode from pre-response
	emojiID := string(msg.PreHandleResp.EmojiID)

	bc.UserLogin.Log.Debug().
		Int64("message_id", messageID).
		Str("emoji_id", emojiID).
		Str("emoji", msg.PreHandleResp.Emoji).
		Msg("Sending reaction to Bookface")

	// Send the reaction
	resp, err := bc.Client.SendReaction(ctx, metadata.UserID, messageID, emojiID)
	if err != nil {
		return nil, fmt.Errorf("failed to send reaction: %w", err)
	}

	bc.UserLogin.Log.Info().
		Int64("message_id", messageID).
		Str("emoji_id", emojiID).
		Msg("Reaction sent successfully")

	// Return the reaction info (bridge will fill in missing fields)
	return &database.Reaction{
		EmojiID:   networkid.EmojiID(resp.EmojiID),
		Emoji:     msg.PreHandleResp.Emoji,
		Timestamp: resp.CreatedAt,
	}, nil
}

// HandleMatrixReactionRemove removes a reaction from Bookface.
func (bc *BookfaceClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	metadata := bc.UserLogin.Metadata.(*UserLoginMetadata)

	// Parse the message ID
	messageID, err := strconv.ParseInt(string(msg.TargetReaction.MessageID), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}

	// Get emoji ID (shortcode)
	emojiID := string(msg.TargetReaction.EmojiID)

	bc.UserLogin.Log.Debug().
		Int64("message_id", messageID).
		Str("emoji_id", emojiID).
		Msg("Removing reaction from Bookface")

	// Remove the reaction
	if err := bc.Client.RemoveReaction(ctx, metadata.UserID, messageID, emojiID); err != nil {
		return fmt.Errorf("failed to remove reaction: %w", err)
	}

	bc.UserLogin.Log.Info().
		Int64("message_id", messageID).
		Str("emoji_id", emojiID).
		Msg("Reaction removed successfully")

	return nil
}

// emojiToShortcode converts a unicode emoji to a shortcode for Bookface.
// This is the reverse of shortcodeToEmoji in backfill.go.
func emojiToShortcode(unicodeEmoji string) string {
	// Build reverse map from unicode to shortcode
	emojiMap := emoji.Map()
	for shortcode, unicode := range emojiMap {
		if unicode == unicodeEmoji {
			// Remove the colons from the shortcode
			return strings.Trim(shortcode, ":")
		}
	}
	// If not found, return the emoji as-is (Bookface might accept unicode)
	return unicodeEmoji
}
