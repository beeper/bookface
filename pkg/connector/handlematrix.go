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
// Bookface uses emoji-mart shortcodes which differ from some library defaults.
func emojiToShortcode(unicodeEmoji string) string {
	// Strip variation selectors (U+FE0E text style, U+FE0F emoji style)
	// that can cause exact string matching to fail
	normalized := strings.ReplaceAll(unicodeEmoji, "\uFE0E", "")
	normalized = strings.ReplaceAll(normalized, "\uFE0F", "")

	// emoji-mart preferred shortcodes - maps unicode to the shortcode Bookface expects
	emojiMartShortcodes := map[string]string{
		"👍": "+1",
		"👎": "-1",
		"❤":  "heart",
		"😀": "grinning",
		"😃": "smiley",
		"😄": "smile",
		"😁": "grin",
		"😆": "laughing",
		"😅": "sweat_smile",
		"🤣": "rofl",
		"😂": "joy",
		"🙂": "slightly_smiling_face",
		"😉": "wink",
		"😊": "blush",
		"😇": "innocent",
		"🥰": "smiling_face_with_three_hearts",
		"😍": "heart_eyes",
		"🤩": "star_struck",
		"😘": "kissing_heart",
		"😗": "kissing",
		"😚": "kissing_closed_eyes",
		"😙": "kissing_smiling_eyes",
		"🥲": "smiling_face_with_tear",
		"😋": "yum",
		"😛": "stuck_out_tongue",
		"😜": "stuck_out_tongue_winking_eye",
		"🤪": "zany_face",
		"😝": "stuck_out_tongue_closed_eyes",
		"🤑": "money_mouth_face",
		"🤗": "hugs",
		"🤭": "hand_over_mouth",
		"🤫": "shushing_face",
		"🤔": "thinking",
		"🤐": "zipper_mouth_face",
		"🤨": "raised_eyebrow",
		"😐": "neutral_face",
		"😑": "expressionless",
		"😶": "no_mouth",
		"😏": "smirk",
		"😒": "unamused",
		"🙄": "roll_eyes",
		"😬": "grimacing",
		"🤥": "lying_face",
		"😌": "relieved",
		"😔": "pensive",
		"😪": "sleepy",
		"🤤": "drooling_face",
		"😴": "sleeping",
		"😷": "mask",
		"🤒": "face_with_thermometer",
		"🤕": "face_with_head_bandage",
		"🤢": "nauseated_face",
		"🤮": "vomiting_face",
		"🤧": "sneezing_face",
		"🥵": "hot_face",
		"🥶": "cold_face",
		"🥴": "woozy_face",
		"😵": "dizzy_face",
		"🤯": "exploding_head",
		"🤠": "cowboy_hat_face",
		"🥳": "partying_face",
		"🥸": "disguised_face",
		"😎": "sunglasses",
		"🤓": "nerd_face",
		"🧐": "monocle_face",
		"😕": "confused",
		"😟": "worried",
		"🙁": "slightly_frowning_face",
		"😮": "open_mouth",
		"😯": "hushed",
		"😲": "astonished",
		"😳": "flushed",
		"🥺": "pleading_face",
		"😦": "frowning",
		"😧": "anguished",
		"😨": "fearful",
		"😰": "cold_sweat",
		"😥": "disappointed_relieved",
		"😢": "cry",
		"😭": "sob",
		"😱": "scream",
		"😖": "confounded",
		"😣": "persevere",
		"😞": "disappointed",
		"😓": "sweat",
		"😩": "weary",
		"😫": "tired_face",
		"🥱": "yawning_face",
		"😤": "triumph",
		"😡": "rage",
		"😠": "angry",
		"🤬": "cursing_face",
		"😈": "smiling_imp",
		"👿": "imp",
		"💀": "skull",
		"💩": "poop",
		"🤡": "clown_face",
		"👹": "japanese_ogre",
		"👺": "japanese_goblin",
		"👻": "ghost",
		"👽": "alien",
		"👾": "space_invader",
		"🤖": "robot",
		"😺": "smiley_cat",
		"😸": "smile_cat",
		"😹": "joy_cat",
		"😻": "heart_eyes_cat",
		"😼": "smirk_cat",
		"😽": "kissing_cat",
		"🙀": "scream_cat",
		"😿": "crying_cat_face",
		"😾": "pouting_cat",
		"🙈": "see_no_evil",
		"🙉": "hear_no_evil",
		"🙊": "speak_no_evil",
		"💋": "kiss",
		"💯": "100",
		"💢": "anger",
		"💥": "boom",
		"💫": "dizzy",
		"💦": "sweat_drops",
		"💨": "dash",
		"🔥": "fire",
		"✨": "sparkles",
		"⭐": "star",
		"🌟": "star2",
		"🎉": "tada",
		"🎊": "confetti_ball",
		"✅": "white_check_mark",
		"❌": "x",
		"❓": "question",
		"❗": "exclamation",
		"💪": "muscle",
		"🙏": "pray",
		"👏": "clap",
		"🤝": "handshake",
		"👋": "wave",
		"✌":  "v",
		"🤞": "crossed_fingers",
		"🤟": "love_you_gesture",
		"🤘": "metal",
		"👌": "ok_hand",
		"🤌": "pinched_fingers",
		"🤏": "pinching_hand",
		"👈": "point_left",
		"👉": "point_right",
		"👆": "point_up_2",
		"👇": "point_down",
		"☝":  "point_up",
		"✋": "hand",
		"🤚": "raised_back_of_hand",
		"🖐": "raised_hand_with_fingers_splayed",
		"🖖": "vulcan_salute",
		"👀": "eyes",
		"👁":  "eye",
		"👅": "tongue",
		"👄": "lips",
	}
	if shortcode, ok := emojiMartShortcodes[normalized]; ok {
		return shortcode
	}

	// Fall back to emoji library lookup
	emojiMap := emoji.Map()
	for shortcode, unicode := range emojiMap {
		if unicode == normalized {
			// Remove the colons from the shortcode
			return strings.Trim(shortcode, ":")
		}
	}
	// If not found, return the emoji as-is (Bookface might accept unicode)
	return unicodeEmoji
}
