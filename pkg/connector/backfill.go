package connector

import (
	"context"
	"fmt"
	"strconv"

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
	if params.AnchorMessage != nil {
		msgID, err := strconv.ParseInt(string(params.AnchorMessage.ID), 10, 64)
		if err == nil {
			if params.Forward {
				afterID = &msgID
				log = log.With().Int64("after_id", msgID).Logger()
			} else {
				beforeID = &msgID
				log = log.With().Int64("before_id", msgID).Logger()
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

	// Convert messages
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
	}
	log.Debug().
		Int("message_count", len(resp.Messages)).
		Bool("has_more", resp.HasMore).
		Msg("Returning backfill response")

	return &resp, nil
}

// convertMessage converts a Bookface message to a bridgev2 BackfillMessage.
func (bc *BookfaceClient) convertMessage(msg *bookface.Message) *bridgev2.BackfillMessage {
	var userID int64
	if msg.User != nil {
		userID = msg.User.ID
	}
	sender := bc.makeEventSender(userID)

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

	return &bridgev2.BackfillMessage{
		ConvertedMessage: &bridgev2.ConvertedMessage{
			Parts: parts,
		},
		Sender:    sender,
		ID:        networkid.MessageID(fmt.Sprintf("%d", msg.ID)),
		Timestamp: msg.CreatedAt,
	}
}
