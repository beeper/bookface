package connector

import (
	"fmt"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

const (
	mediaIDTypeAvatar = 254
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

// ParsedMediaID contains the parsed information from a media ID.
type ParsedMediaID struct {
	AvatarURL string
}

// ParseMediaID parses a media ID and returns the avatar URL.
func ParseMediaID(mediaID networkid.MediaID) (*ParsedMediaID, error) {
	if len(mediaID) < 2 {
		return nil, fmt.Errorf("media ID too short")
	}
	if mediaID[0] != mediaIDTypeAvatar {
		return nil, fmt.Errorf("unknown media ID type %d", mediaID[0])
	}
	return &ParsedMediaID{
		AvatarURL: string(mediaID[1:]),
	}, nil
}
