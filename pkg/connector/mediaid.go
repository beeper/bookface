package connector

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

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
// Format: type byte + varint(len(userLoginID)) + userLoginID + varint(len(url)) + url
func MakeAttachmentMediaID(userLoginID networkid.UserLoginID, attachmentURL string) networkid.MediaID {
	mediaID := []byte{mediaIDTypeAttachment}
	mediaID = appendLengthPrefixed(mediaID, []byte(userLoginID))
	mediaID = appendLengthPrefixed(mediaID, []byte(attachmentURL))
	return mediaID
}

func appendLengthPrefixed(buf, data []byte) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(data)))
	return append(buf, data...)
}

func readLengthPrefixed(r *bytes.Reader) (string, error) {
	length, err := binary.ReadUvarint(r)
	if err != nil {
		return "", err
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return "", err
	}
	return string(data), nil
}

// ParsedMediaID contains the parsed information from a media ID.
type ParsedMediaID struct {
	Type          byte
	AvatarURL     string
	UserLoginID   networkid.UserLoginID
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
		r := bytes.NewReader(mediaID[1:])
		userLoginID, err := readLengthPrefixed(r)
		if err != nil {
			return nil, fmt.Errorf("failed to read user login ID: %w", err)
		}
		attachmentURL, err := readLengthPrefixed(r)
		if err != nil {
			return nil, fmt.Errorf("failed to read attachment URL: %w", err)
		}
		return &ParsedMediaID{
			Type:          mediaIDTypeAttachment,
			UserLoginID:   networkid.UserLoginID(userLoginID),
			AttachmentURL: attachmentURL,
		}, nil
	default:
		return nil, fmt.Errorf("unknown media ID type %d", mediaID[0])
	}
}
