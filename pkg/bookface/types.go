package bookface

import (
	"time"
)

// User represents a Bookface user.
type User struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	ShortName   string `json:"short_name"`
	HNID        string `json:"hnid"`
	Slug        string `json:"slug"`
	AvatarThumb string `json:"avatar_thumb"`
	AvatarURL   string `json:"avatar_url"`

	// Company info from byline_company
	BylineCompany *UserCompany `json:"byline_company,omitempty"`
}

// UserCompany represents company info in a user's byline.
type UserCompany struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Logo  string `json:"logo"`
	Batch string `json:"batch"`
}

// GetDisplayName returns the best display name for the user.
func (u *User) GetDisplayName() string {
	if u.FullName != "" {
		return u.FullName
	}
	if u.Name != "" {
		return u.Name
	}
	if u.ShortName != "" {
		return u.ShortName
	}
	return u.HNID
}

// GetAvatarURL returns the avatar URL if available.
func (u *User) GetAvatarURL() string {
	if u.AvatarThumb != "" {
		return u.AvatarThumb
	}
	return u.AvatarURL
}

// ChatUser represents a user within a chat context.
type ChatUser struct {
	User    *User  `json:"user"`
	Role    string `json:"role"`
	Deleted bool   `json:"deleted"`
}

// MediaUpload represents an uploaded file/image in a message.
type MediaUpload struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
	ThumbURL    string `json:"thumb_url"`
}

// ReactionUser represents the minimal user info in a reaction.
type ReactionUser struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	CurrentCompanyName string `json:"current_company_name"`
}

// Reaction represents a reaction on a message.
type Reaction struct {
	EmojiID        string        `json:"emoji_id"`
	EmojiVariant   *string       `json:"emoji_variant"`
	ReactionableID int64         `json:"reactionable_id"`
	User           *ReactionUser `json:"user,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
}

// Message represents a chat message.
type Message struct {
	ID           int64         `json:"id"`
	ChatID       int64         `json:"chat_id"`
	UserID       int64         `json:"user_id"`
	Content      string        `json:"content"`
	HTMLContent  string        `json:"html_content"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	User         *User         `json:"user,omitempty"`
	MediaUploads []MediaUpload `json:"media_uploads,omitempty"`
	Reactions    []Reaction    `json:"reactions,omitempty"`
	ThreadID     *int64        `json:"thread_id,omitempty"`
}

// Chat represents a conversation/chat room.
type Chat struct {
	ID                 int64      `json:"id"`
	Name               string     `json:"name"`
	Path               string     `json:"path"`
	IsDM               bool       `json:"is_dm"`
	Visibility         string     `json:"visibility"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ChatUsers          []ChatUser `json:"chat_users,omitempty"`
	InitialLastMessage *Message   `json:"initial_last_message,omitempty"`
}

// ChatsResponse is the response from /messages.json.
type ChatsResponse struct {
	Chats          []Chat  `json:"chats"`
	NextCursor     *string `json:"next_cursor"`
	RemainingCount int     `json:"remaining_count"`
}

// MessagesResponse is the response from /messages/<chat_id>/chat_messages.
type MessagesResponse struct {
	Messages      []Message `json:"messages"`
	HasMoreBefore bool      `json:"has_more_before"`
	HasMoreAfter  bool      `json:"has_more_after"`
}

// ChatUsersResponse is the response from /messages/<chat_id>/chat_users.
type ChatUsersResponse struct {
	ChatID    int64      `json:"chat_id"`
	ChatUsers []ChatUser `json:"chat_users"`
}

// ApplicationShellResponse is the response from /messages/application_shell.json.
type ApplicationShellResponse struct {
	CurrentUser *User `json:"current_user"`
}

// Credentials holds the authentication data for a Bookface session.
type Credentials struct {
	Cookies   string `json:"cookies"`
	CSRFToken string `json:"csrf_token"`
}

// SendMessageRequest is the request body for sending a message.
type SendMessageRequest struct {
	ChatMessage SendMessagePayload `json:"chat_message"`
}

// SendMessagePayload is the inner payload for sending a message.
type SendMessagePayload struct {
	ChatID          int64    `json:"chat_id"`
	Content         string   `json:"content"`
	MediaUploads    []any    `json:"media_uploads"`
	ClientMessageID string   `json:"client_message_id"`
	VersionNumber   int      `json:"version_number"`
	ThreadID        *int64   `json:"thread_id"`
	Referrer        string   `json:"referrer"`
}

// EditMessageRequest is the request body for editing a message.
type EditMessageRequest struct {
	Content         string `json:"content"`
	MessageID       int64  `json:"message_id"`
	ClientMessageID string `json:"client_message_id"`
	ClientID        string `json:"client_id"`
	VersionNumber   int    `json:"version_number"`
}

// DeleteMessageRequest is the request body for deleting a message.
type DeleteMessageRequest struct {
	MessageID       int64  `json:"message_id"`
	ClientMessageID string `json:"client_message_id"`
}

// SendReactionRequest is the request body for adding a reaction.
type SendReactionRequest struct {
	EmojiID         string  `json:"emoji_id"`
	EmojiVariant    *string `json:"emoji_variant"`
	UserID          int64   `json:"user_id"`
	ReactionableID  int64   `json:"reactionable_id"`
	ReactionableType string `json:"reactionable_type"`
	ClientID        string  `json:"client_id"`
}

// SendReactionResponse is the response from adding a reaction.
type SendReactionResponse struct {
	EmojiID          string        `json:"emoji_id"`
	EmojiVariant     *string       `json:"emoji_variant"`
	ReactionableType string        `json:"reactionable_type"`
	ReactionableID   int64         `json:"reactionable_id"`
	User             *ReactionUser `json:"user"`
	CreatedAt        time.Time     `json:"created_at"`
}
