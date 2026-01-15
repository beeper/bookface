package connector

import (
	"fmt"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

const (
	mediaIDTypeAvatar     = 254
	mediaIDTypeAttachment = 253
)

// MakeAvatarMediaID creates a media ID for an avatar URL.
// Since Bookface avatar URLs are stable S3 URLs that don't expire,
// we simply encode the URL directly.
func MakeAvatarMediaID(avatarURL string) networkid.MediaID {
	mediaID := make([]byte, 0, 1+len(avatarURL))
	mediaID = append(mediaID, mediaIDTypeAvatar)
	mediaID = append(mediaID, avatarURL...)
	return mediaID
}

// MakeAttachmentMediaID creates a media ID for a message attachment URL.
// Bookface attachment URLs require authentication to access.
func MakeAttachmentMediaID(attachmentURL string) networkid.MediaID {
	mediaID := make([]byte, 0, 1+len(attachmentURL))
	mediaID = append(mediaID, mediaIDTypeAttachment)
	mediaID = append(mediaID, attachmentURL...)
	return mediaID
}

// ParsedMediaID contains the parsed information from a media ID.
type ParsedMediaID struct {
	Type          byte
	AvatarURL     string
	AttachmentURL string
}

// ParseMediaID parses a media ID and returns the URL and type.
func ParseMediaID(mediaID networkid.MediaID) (*ParsedMediaID, error) {
	if len(mediaID) < 2 {
		return nil, fmt.Errorf("media ID too short")
	}
	switch mediaID[0] {
	case mediaIDTypeAvatar:
		return &ParsedMediaID{
			Type:      mediaIDTypeAvatar,
			AvatarURL: string(mediaID[1:]),
		}, nil
	case mediaIDTypeAttachment:
		return &ParsedMediaID{
			Type:          mediaIDTypeAttachment,
			AttachmentURL: string(mediaID[1:]),
		}, nil
	default:
		return nil, fmt.Errorf("unknown media ID type %d", mediaID[0])
	}
}
