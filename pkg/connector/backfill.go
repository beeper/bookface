package connector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"

	"github.com/enescakir/emoji"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/bookface/pkg/bookface"
)

var _ bridgev2.BackfillingNetworkAPI = (*BookfaceClient)(nil)

// getMessageMetadata retrieves the stored metadata for a message.
func (bc *BookfaceClient) getMessageMetadata(ctx context.Context, msgID int64) *MessageMetadata {
	messageID := networkid.MessageID(fmt.Sprintf("%d", msgID))
	existingMsg, err := bc.UserLogin.Bridge.DB.Message.GetFirstPartByID(ctx, bc.UserLogin.ID, messageID)
	if err != nil || existingMsg == nil {
		return nil
	}
	if existingMsg.Metadata == nil {
		return nil
	}
	metadata, ok := existingMsg.Metadata.(*MessageMetadata)
	if !ok {
		return nil
	}
	return metadata
}

// updateMessageMetadata updates the metadata for a message in the database.
// If the message doesn't exist yet (e.g., during backfill), returns an error.
func (bc *BookfaceClient) updateMessageMetadata(ctx context.Context, msgID int64, metadata *MessageMetadata) error {
	messageID := networkid.MessageID(fmt.Sprintf("%d", msgID))
	existingMsg, err := bc.UserLogin.Bridge.DB.Message.GetFirstPartByID(ctx, bc.UserLogin.ID, messageID)
	if err != nil || existingMsg == nil {
		return fmt.Errorf("message not found: %d", msgID)
	}
	existingMsg.Metadata = metadata
	return bc.UserLogin.Bridge.DB.Message.Update(ctx, existingMsg)
}

// storePendingMetadata stores metadata to be applied after backfill completes.
func (bc *BookfaceClient) storePendingMetadata(msgID int64, metadata *MessageMetadata) {
	if bc.pendingMetadataUpdates == nil {
		bc.pendingMetadataUpdates = make(map[int64]*MessageMetadata)
	}
	bc.pendingMetadataUpdates[msgID] = metadata
}

// applyPendingMetadataUpdates applies any pending metadata updates from previous backfills.
// This should be called at the start of each backfill to ensure metadata is saved
// for messages that were created in the previous backfill cycle.
func (bc *BookfaceClient) applyPendingMetadataUpdates(ctx context.Context, log zerolog.Logger) {
	if len(bc.pendingMetadataUpdates) == 0 {
		return
	}

	log.Info().Int("count", len(bc.pendingMetadataUpdates)).Msg("Applying pending metadata updates from previous backfill")

	applied := 0
	for msgID, metadata := range bc.pendingMetadataUpdates {
		if err := bc.updateMessageMetadata(ctx, msgID, metadata); err != nil {
			log.Debug().Err(err).Int64("msg_id", msgID).Msg("Failed to apply pending metadata (message may not exist yet)")
		} else {
			applied++
			delete(bc.pendingMetadataUpdates, msgID)
		}
	}

	log.Info().
		Int("applied", applied).
		Int("remaining", len(bc.pendingMetadataUpdates)).
		Msg("Finished applying pending metadata updates")
}

// shouldResyncThreads checks if we need to resync thread messages based on updated_at.
// Returns true if the message's updated_at differs from what we have stored.
func (bc *BookfaceClient) shouldResyncThreads(ctx context.Context, msg *bookface.Message) bool {
	if msg.ThreadReplyCount == 0 {
		// Check if we had threads before that are now deleted
		metadata := bc.getMessageMetadata(ctx, msg.ID)
		if metadata != nil && len(metadata.ThreadMessageIDs) > 0 {
			bc.UserLogin.Log.Info().
				Int64("msg_id", msg.ID).
				Int("stored_thread_count", len(metadata.ThreadMessageIDs)).
				Msg("Thread count is now 0 but we have stored threads, need to redact")
			return true
		}
		return false
	}

	metadata := bc.getMessageMetadata(ctx, msg.ID)
	if metadata == nil {
		// No stored metadata - this is a new message with threads
		bc.UserLogin.Log.Debug().
			Int64("msg_id", msg.ID).
			Int("thread_reply_count", msg.ThreadReplyCount).
			Msg("No stored metadata, will fetch threads")
		return true
	}

	apiUpdatedAt := msg.UpdatedAt.Unix()
	if metadata.UpdatedAt != apiUpdatedAt {
		bc.UserLogin.Log.Info().
			Int64("msg_id", msg.ID).
			Int64("stored_updated_at", metadata.UpdatedAt).
			Int64("api_updated_at", apiUpdatedAt).
			Int("thread_reply_count", msg.ThreadReplyCount).
			Msg("Message updated_at changed, will resync threads")
		return true
	}

	bc.UserLogin.Log.Debug().
		Int64("msg_id", msg.ID).
		Msg("Message unchanged, skipping thread sync")
	return false
}

// FetchMessages implements backfill support by fetching messages from the Bookface API.
func (bc *BookfaceClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	log := zerolog.Ctx(ctx).With().Str("method", "FetchMessages").Logger()
	ctx = log.WithContext(ctx)

	// Apply any pending metadata updates from previous backfills before proceeding.
	// This ensures metadata is saved for messages created in the previous cycle.
	bc.applyPendingMetadataUpdates(ctx, log)

	chatID, err := strconv.ParseInt(string(params.Portal.ID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid chat ID: %w", err)
	}

	// Log detailed params for debugging
	var anchorMsgID string
	if params.AnchorMessage != nil {
		anchorMsgID = string(params.AnchorMessage.ID)
	}
	log.Info().
		Int64("chat_id", chatID).
		Bool("forward", params.Forward).
		Int("count", params.Count).
		Str("cursor", string(params.Cursor)).
		Str("anchor_message_id", anchorMsgID).
		Msg("FetchMessages called")

	resp := bridgev2.FetchMessagesResponse{
		Forward: params.Forward,
	}

	if params.Forward {
		return bc.forwardBackfill(ctx, log, chatID, params)
	}

	// Backward backfill: use before_id for pagination
	var beforeID *int64
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

	apiResp, err := bc.Client.GetMessages(ctx, chatID, beforeID, nil)
	if err != nil {
		return nil, err
	}

	if len(apiResp.Messages) == 0 {
		return &bridgev2.FetchMessagesResponse{HasMore: false, Forward: false}, nil
	}

	log.Info().Int("api_message_count", len(apiResp.Messages)).Msg("Got messages from API (backward)")

	allMessages, redactIDs := bc.fetchThreadsForMessages(ctx, log, chatID, params.Portal, apiResp.Messages)

	// Track oldest message ID for cursor
	var oldestMsgID int64
	for _, msg := range allMessages {
		if oldestMsgID == 0 || msg.ID < oldestMsgID {
			oldestMsgID = msg.ID
		}
	}

	// Convert messages
	for i := range allMessages {
		msg := &allMessages[i]
		converted := bc.convertMessage(msg)
		if converted != nil {
			resp.Messages = append(resp.Messages, converted)
		}
	}

	// Queue redactions for deleted thread messages
	if len(redactIDs) > 0 {
		bc.queueThreadRedactions(ctx, log, params.Portal, redactIDs)
	}

	resp.HasMore = apiResp.HasMoreBefore
	if oldestMsgID > 0 {
		resp.Cursor = networkid.PaginationCursor(strconv.FormatInt(oldestMsgID, 10))
	}

	log.Debug().
		Int("message_count", len(resp.Messages)).
		Bool("has_more", resp.HasMore).
		Str("cursor", string(resp.Cursor)).
		Int("redactions", len(redactIDs)).
		Msg("Returning backward backfill response")

	return &resp, nil
}

// forwardBackfill fetches recent messages and paginates backwards until we overlap
// with messages we already have in the database.
func (bc *BookfaceClient) forwardBackfill(ctx context.Context, log zerolog.Logger, chatID int64, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	log.Info().Msg("Forward backfill: fetching recent messages and checking for thread updates")

	resp := bridgev2.FetchMessagesResponse{
		Forward: true,
	}

	var allMessages []bookface.Message
	var allRedactIDs []int64
	var beforeID *int64
	pageCount := 0
	maxPages := 10 // Safety limit to prevent infinite loops

	for pageCount < maxPages {
		pageCount++

		apiResp, err := bc.Client.GetMessages(ctx, chatID, beforeID, nil)
		if err != nil {
			return nil, err
		}

		if len(apiResp.Messages) == 0 {
			log.Info().Int("pages_fetched", pageCount).Msg("No more messages to fetch")
			break
		}

		log.Info().
			Int("page", pageCount).
			Int("message_count", len(apiResp.Messages)).
			Msg("Fetched page of messages")

		// Check if any message in this page already exists in our DB
		foundOverlap := false
		for _, msg := range apiResp.Messages {
			messageID := networkid.MessageID(fmt.Sprintf("%d", msg.ID))
			existingMsg, err := bc.UserLogin.Bridge.DB.Message.GetFirstPartByID(ctx, bc.UserLogin.ID, messageID)
			if err == nil && existingMsg != nil {
				log.Info().
					Int64("msg_id", msg.ID).
					Int("page", pageCount).
					Msg("Found overlap with existing message, stopping pagination")
				foundOverlap = true
				break
			}
		}

		// Fetch threads for messages in this page
		pageMessages, redactIDs := bc.fetchThreadsForMessages(ctx, log, chatID, params.Portal, apiResp.Messages)
		allMessages = append(allMessages, pageMessages...)
		allRedactIDs = append(allRedactIDs, redactIDs...)

		if foundOverlap || !apiResp.HasMoreBefore {
			log.Info().
				Int("pages_fetched", pageCount).
				Bool("found_overlap", foundOverlap).
				Bool("has_more_before", apiResp.HasMoreBefore).
				Msg("Stopping forward backfill pagination")
			break
		}

		// Find oldest message ID for next page
		var oldestID int64
		for _, msg := range apiResp.Messages {
			if oldestID == 0 || msg.ID < oldestID {
				oldestID = msg.ID
			}
		}
		beforeID = &oldestID
	}

	if len(allMessages) == 0 {
		return &bridgev2.FetchMessagesResponse{HasMore: false, Forward: true}, nil
	}

	// Sort all messages by timestamp
	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].CreatedAt.Before(allMessages[j].CreatedAt)
	})

	// Convert messages
	for i := range allMessages {
		msg := &allMessages[i]
		converted := bc.convertMessage(msg)
		if converted != nil {
			resp.Messages = append(resp.Messages, converted)
		}
	}

	// Queue redactions for deleted thread messages
	if len(allRedactIDs) > 0 {
		bc.queueThreadRedactions(ctx, log, params.Portal, allRedactIDs)
	}

	resp.HasMore = false // Forward backfill completes in one call

	log.Info().
		Int("total_messages", len(resp.Messages)).
		Int("pages_fetched", pageCount).
		Int("redactions", len(allRedactIDs)).
		Msg("Forward backfill complete")

	return &resp, nil
}

// queueThreadRedactions queues redaction events for deleted thread messages.
func (bc *BookfaceClient) queueThreadRedactions(ctx context.Context, log zerolog.Logger, portal *bridgev2.Portal, messageIDs []int64) {
	if portal == nil {
		log.Warn().Msg("Cannot queue redactions: portal is nil")
		return
	}

	portalKey := networkid.PortalKey{
		ID:       portal.ID,
		Receiver: bc.UserLogin.ID,
	}

	for _, msgID := range messageIDs {
		messageID := networkid.MessageID(fmt.Sprintf("%d", msgID))

		log.Info().
			Int64("msg_id", msgID).
			Msg("Queueing redaction for deleted thread message")

		bc.UserLogin.QueueRemoteEvent(&simplevent.MessageRemove{
			EventMeta: simplevent.EventMeta{
				Type:      bridgev2.RemoteEventMessageRemove,
				PortalKey: portalKey,
			},
			TargetMessage: messageID,
		})
	}
}

// threadSyncResult contains the result of syncing threads for a message.
type threadSyncResult struct {
	newMessages      []bookface.Message
	redactMessageIDs []int64
}

// syncThreadsForMessage syncs thread messages for a single message.
// Returns new messages to add and IDs of messages to redact.
func (bc *BookfaceClient) syncThreadsForMessage(ctx context.Context, log zerolog.Logger, chatID int64, msg *bookface.Message) *threadSyncResult {
	result := &threadSyncResult{}

	// Get stored metadata to find previously known thread message IDs
	storedMetadata := bc.getMessageMetadata(ctx, msg.ID)
	var storedThreadIDs []int64
	if storedMetadata != nil {
		storedThreadIDs = storedMetadata.ThreadMessageIDs
	}

	// If thread_reply_count is 0, all threads were deleted
	if msg.ThreadReplyCount == 0 {
		if len(storedThreadIDs) > 0 {
			log.Info().
				Int64("msg_id", msg.ID).
				Int("redacting_count", len(storedThreadIDs)).
				Msg("All threads deleted, redacting stored thread messages")
			result.redactMessageIDs = storedThreadIDs

			// Update metadata to clear thread IDs
			newMetadata := &MessageMetadata{
				UpdatedAt:        msg.UpdatedAt.Unix(),
				ThreadMessageIDs: nil,
			}
			if err := bc.updateMessageMetadata(ctx, msg.ID, newMetadata); err != nil {
				log.Debug().Err(err).Int64("msg_id", msg.ID).Msg("Message not in DB yet, storing pending metadata")
				bc.storePendingMetadata(msg.ID, newMetadata)
			}
		}
		return result
	}

	// Fetch current thread messages from API
	threadResp, err := bc.Client.GetThreadMessages(ctx, chatID, msg.ID)
	if err != nil {
		log.Warn().Err(err).Int64("thread_id", msg.ID).Msg("Failed to fetch thread messages")
		return result
	}

	// Build set of current thread message IDs from API
	currentThreadIDs := make(map[int64]bool)
	var newThreadIDs []int64
	for _, threadMsg := range threadResp.Messages {
		if threadMsg.ID != msg.ID { // Skip the parent message
			currentThreadIDs[threadMsg.ID] = true
			newThreadIDs = append(newThreadIDs, threadMsg.ID)
			result.newMessages = append(result.newMessages, threadMsg)
		}
	}

	// Find thread messages that were deleted (in stored but not in API)
	storedSet := make(map[int64]bool)
	for _, id := range storedThreadIDs {
		storedSet[id] = true
		if !currentThreadIDs[id] {
			result.redactMessageIDs = append(result.redactMessageIDs, id)
		}
	}

	// Log what we're doing
	newCount := 0
	for id := range currentThreadIDs {
		if !storedSet[id] {
			newCount++
		}
	}

	log.Info().
		Int64("thread_parent_id", msg.ID).
		Int("api_thread_count", len(currentThreadIDs)).
		Int("stored_thread_count", len(storedThreadIDs)).
		Int("new_threads", newCount).
		Int("deleted_threads", len(result.redactMessageIDs)).
		Msg("Synced thread messages")

	// Update metadata with current thread IDs and updated_at
	newMetadata := &MessageMetadata{
		UpdatedAt:        msg.UpdatedAt.Unix(),
		ThreadMessageIDs: newThreadIDs,
	}
	if err := bc.updateMessageMetadata(ctx, msg.ID, newMetadata); err != nil {
		log.Debug().Err(err).Int64("msg_id", msg.ID).Msg("Message not in DB yet, storing pending metadata")
		bc.storePendingMetadata(msg.ID, newMetadata)
	}

	return result
}

// fetchThreadsForMessages checks each message for thread changes and syncs thread messages.
// Returns all messages (including thread messages) and IDs of messages to redact.
func (bc *BookfaceClient) fetchThreadsForMessages(ctx context.Context, log zerolog.Logger, chatID int64, portal *bridgev2.Portal, messages []bookface.Message) ([]bookface.Message, []int64) {
	allMessages := make([]bookface.Message, 0, len(messages))
	allMessages = append(allMessages, messages...)
	var allRedactIDs []int64

	for i := range messages {
		msg := &messages[i]
		if bc.shouldResyncThreads(ctx, msg) {
			result := bc.syncThreadsForMessage(ctx, log, chatID, msg)
			allMessages = append(allMessages, result.newMessages...)
			allRedactIDs = append(allRedactIDs, result.redactMessageIDs...)
		}
	}

	return allMessages, allRedactIDs
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
	var parts []*bridgev2.ConvertedMessagePart

	// Add text content first if present
	if msg.Content != "" {
		content := &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    msg.Content,
		}
		if msg.HTMLContent != "" {
			content.Format = event.FormatHTML
			content.FormattedBody = msg.HTMLContent
		}
		parts = append(parts, &bridgev2.ConvertedMessagePart{
			ID:      networkid.PartID(""),
			Type:    event.EventMessage,
			Content: content,
		})
	}

	// Add each media upload as a separate part
	for i, media := range msg.MediaUploads {
		mediaContent, err := bc.convertMediaToContent(ctx, &media)
		if err != nil {
			bc.UserLogin.Log.Warn().Err(err).Int64("media_id", media.ID).Msg("Failed to convert media")
			continue
		}
		partID := networkid.PartID(fmt.Sprintf("media_%d", i))
		parts = append(parts, &bridgev2.ConvertedMessagePart{
			ID:      partID,
			Type:    event.EventMessage,
			Content: mediaContent,
		})
	}

	// If no parts were created (empty message), create an empty text part
	if len(parts) == 0 {
		parts = append(parts, &bridgev2.ConvertedMessagePart{
			ID:   networkid.PartID(""),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    "",
			},
		})
	}

	converted := &bridgev2.ConvertedMessage{Parts: parts}

	// Handle thread replies
	if msg.ThreadID != nil {
		threadRoot := networkid.MessageID(fmt.Sprintf("%d", *msg.ThreadID))
		converted.ThreadRoot = &threadRoot
	}

	return converted, nil
}

// convertMediaToContent converts a Bookface media upload to a Matrix message content.
func (bc *BookfaceClient) convertMediaToContent(ctx context.Context, media *bookface.MediaUpload) (*event.MessageEventContent, error) {
	connector := bc.UserLogin.Bridge.Network.(*BookfaceConnector)

	// Determine message type based on content type or dimensions
	msgType := event.MsgFile
	mimeType := media.ContentType
	if media.Width > 0 && media.Height > 0 {
		msgType = event.MsgImage
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
	}

	// Generate filename if not provided
	filename := media.Filename
	if filename == "" {
		if msgType == event.MsgImage {
			filename = fmt.Sprintf("image_%d.jpg", media.ID)
		} else {
			filename = fmt.Sprintf("file_%d", media.ID)
		}
	}

	content := &event.MessageEventContent{
		MsgType: msgType,
		Body:    filename,
		Info: &event.FileInfo{
			Width:    media.Width,
			Height:   media.Height,
			MimeType: mimeType,
		},
	}

	// Use the URL with variant=large for images, or original_url for files
	mediaURL := media.URL
	if mediaURL == "" {
		mediaURL = media.OriginalURL
	}

	if connector.DirectMedia {
		// Generate direct media URI
		mediaID := MakeAttachmentMediaID(mediaURL)
		mxc, err := bc.UserLogin.Bridge.Matrix.GenerateContentURI(ctx, mediaID)
		if err != nil {
			bc.UserLogin.Log.Warn().Err(err).Str("url", mediaURL).Msg("Failed to generate direct media URI, falling back to upload")
		} else {
			content.URL = mxc
			return content, nil
		}
	}

	// Fallback: download and upload the media
	data, err := bc.downloadMedia(ctx, mediaURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download media: %w", err)
	}

	content.URL, content.File, err = bc.UserLogin.Bridge.Bot.UploadMedia(ctx, "", data, filename, mimeType)
	if err != nil {
		return nil, fmt.Errorf("failed to upload media: %w", err)
	}

	return content, nil
}

// downloadMedia fetches media from a URL.
func (bc *BookfaceClient) downloadMedia(ctx context.Context, mediaURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download media: status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
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
