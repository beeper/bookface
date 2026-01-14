package connector

import (
	"context"
	"fmt"
	"strconv"

	"github.com/enescakir/emoji"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/bookface/pkg/bookface"
)

var _ bridgev2.BackfillingNetworkAPI = (*BookfaceClient)(nil)

// FetchMessages implements backfill support by fetching messages from the Bookface API.
func (bc *BookfaceClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	log := zerolog.Ctx(ctx).With().Str("method", "FetchMessages").Logger()
	ctx = log.WithContext(ctx)

	chatID, err := strconv.ParseInt(string(params.Portal.ID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid chat ID: %w", err)
	}

	log.Debug().
		Int64("chat_id", chatID).
		Bool("forward", params.Forward).
		Int("count", params.Count).
		Msg("FetchMessages called")

	resp := bridgev2.FetchMessagesResponse{
		Forward: params.Forward,
	}

	// Determine pagination based on direction
	var beforeID, afterID *int64

	if params.Forward {
		// Forward backfill: use AnchorMessage to get messages after it
		if params.AnchorMessage != nil {
			msgID, err := strconv.ParseInt(string(params.AnchorMessage.ID), 10, 64)
			if err == nil {
				afterID = &msgID
				log = log.With().Int64("after_id", msgID).Logger()
			}
		}
	} else {
		// Backward backfill: prefer Cursor (from previous batch), fall back to AnchorMessage
		// The Cursor is persisted by mautrix-go's backfill_task table for resumption
		if params.Cursor != "" {
			msgID, err := strconv.ParseInt(string(params.Cursor), 10, 64)
			if err == nil {
				beforeID = &msgID
				log = log.With().Int64("before_id_from_cursor", msgID).Logger()
			}
		} else if params.AnchorMessage != nil {
			msgID, err := strconv.ParseInt(string(params.AnchorMessage.ID), 10, 64)
			if err == nil {
				beforeID = &msgID
				log = log.With().Int64("before_id_from_anchor", msgID).Logger()
			}
		}
	}

	// Fetch messages from API
	apiResp, err := bc.Client.GetMessages(ctx, chatID, beforeID, afterID)
	if err != nil {
		return nil, err
	}

	if len(apiResp.Messages) == 0 {
		return &bridgev2.FetchMessagesResponse{HasMore: false, Forward: params.Forward}, nil
	}

	log.Debug().Int("api_message_count", len(apiResp.Messages)).Msg("Got messages from API")

	// Track oldest message ID for cursor (even if conversion fails)
	var oldestMsgID int64
	for _, msg := range apiResp.Messages {
		if oldestMsgID == 0 || msg.ID < oldestMsgID {
			oldestMsgID = msg.ID
		}
	}

	// Convert messages (API returns them in chronological order already)
	for i := range apiResp.Messages {
		msg := &apiResp.Messages[i]
		converted := bc.convertMessage(msg)
		if converted != nil {
			resp.Messages = append(resp.Messages, converted)
		}
	}

	// Use correct HasMore based on direction
	if params.Forward {
		resp.HasMore = apiResp.HasMoreAfter
	} else {
		resp.HasMore = apiResp.HasMoreBefore
		// Set cursor to oldest message ID for backward backfill resumption
		// mautrix-go persists this in backfill_task table and passes it back on next call
		if oldestMsgID > 0 {
			resp.Cursor = networkid.PaginationCursor(strconv.FormatInt(oldestMsgID, 10))
		}
	}

	log.Debug().
		Int("message_count", len(resp.Messages)).
		Bool("has_more", resp.HasMore).
		Str("cursor", string(resp.Cursor)).
		Msg("Returning backfill response")

	return &resp, nil
}

// shortcodeToEmoji converts emoji-mart shortcodes to unicode emoji.
func shortcodeToEmoji(shortcode string) string {
	// emoji library expects format ":shortcode:"
	parsed := emoji.Parse(":" + shortcode + ":")
	// If parsing failed (returned unchanged), use shortcode as-is
	if parsed == ":"+shortcode+":" {
		return shortcode
	}
	return parsed
}

// convertToMatrix converts a Bookface message to a bridgev2 ConvertedMessage.
// This is the shared conversion function used by both backfill and real-time events.
func (bc *BookfaceClient) convertToMatrix(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg *bookface.Message) (*bridgev2.ConvertedMessage, error) {
	// Build text content
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    msg.Content,
	}
	if msg.HTMLContent != "" {
		content.Format = event.FormatHTML
		content.FormattedBody = msg.HTMLContent
	}

	parts := []*bridgev2.ConvertedMessagePart{{
		ID:      networkid.PartID(""),
		Type:    event.EventMessage,
		Content: content,
	}}

	// Handle media uploads
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

	return &bridgev2.ConvertedMessage{Parts: parts}, nil
}

// convertReactionsToBackfill converts Bookface reactions to BackfillReaction slice.
func (bc *BookfaceClient) convertReactionsToBackfill(reactions []bookface.Reaction) []*bridgev2.BackfillReaction {
	var result []*bridgev2.BackfillReaction
	for _, reaction := range reactions {
		if reaction.User != nil {
			result = append(result, &bridgev2.BackfillReaction{
				Sender:    bc.makeEventSender(reaction.User.ID),
				Emoji:     shortcodeToEmoji(reaction.EmojiID),
				EmojiID:   networkid.EmojiID(reaction.EmojiID),
				Timestamp: reaction.CreatedAt,
			})
		}
	}
	return result
}

// convertMessage converts a Bookface message to a bridgev2 BackfillMessage.
func (bc *BookfaceClient) convertMessage(msg *bookface.Message) *bridgev2.BackfillMessage {
	var userID int64
	if msg.User != nil {
		userID = msg.User.ID
	}

	converted, err := bc.convertToMatrix(context.Background(), nil, nil, msg)
	if err != nil {
		bc.UserLogin.Log.Warn().Err(err).Int64("message_id", msg.ID).Msg("Failed to convert message to Matrix format")
		return nil
	}

	return &bridgev2.BackfillMessage{
		ConvertedMessage: converted,
		Sender:           bc.makeEventSender(userID),
		ID:               networkid.MessageID(fmt.Sprintf("%d", msg.ID)),
		Timestamp:        msg.CreatedAt,
		Reactions:        bc.convertReactionsToBackfill(msg.Reactions),
	}
}
