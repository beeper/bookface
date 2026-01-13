package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/mediaproxy"
)

var _ bridgev2.DirectMediableNetwork = (*BookfaceConnector)(nil)

// SetUseDirectMedia is called when direct media is enabled.
// It sets a flag so that avatar generation uses direct media URIs.
func (bc *BookfaceConnector) SetUseDirectMedia() {
	bc.DirectMedia = true
}

// Download handles fetching media when a Matrix client requests a direct media URL.
// For Bookface avatars, this returns a redirect to the original S3 URL since
// the URLs are publicly accessible and don't expire.
func (bc *BookfaceConnector) Download(ctx context.Context, mediaID networkid.MediaID, params map[string]string) (mediaproxy.GetMediaResponse, error) {
	parsedID, err := ParseMediaID(mediaID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse media ID: %w", err)
	}

	if parsedID.AvatarURL == "" {
		return nil, fmt.Errorf("empty avatar URL in media ID")
	}

	// Return a redirect response to the original S3 URL.
	// Since Bookface avatar URLs are stable and don't require authentication,
	// we can redirect the client directly to the source.
	return &mediaproxy.GetMediaResponseURL{
		URL: parsedID.AvatarURL,
		// ExpiresAt is zero, meaning no expiration (S3 URLs are stable)
	}, nil
}
