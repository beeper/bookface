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

// Reaction represents a reaction on a message.
type Reaction struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	UserID    int64  `json:"user_id"`
	Emoji     string `json:"emoji"`
	User      *User  `json:"user,omitempty"`
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

// ApplicationShellResponse is the response from /messages/application_shell.json.
type ApplicationShellResponse struct {
	CurrentUser *User `json:"current_user"`
}

// Credentials holds the authentication data for a Bookface session.
type Credentials struct {
	Cookies   string `json:"cookies"`
	CSRFToken string `json:"csrf_token"`
}
