package connector

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/mediaproxy"

	"github.com/beeper/bookface/pkg/bookface"
)

var _ bridgev2.DirectMediableNetwork = (*BookfaceConnector)(nil)

// SetUseDirectMedia is called when direct media is enabled.
func (bc *BookfaceConnector) SetUseDirectMedia() {
	bc.DirectMedia = true
}

// Download handles fetching media when a Matrix client requests a direct media URL.
// Avatar URLs (S3) are publicly accessible and can be redirected.
// Attachment URLs (bookface.ycombinator.com/media) require authentication.
func (bc *BookfaceConnector) Download(ctx context.Context, mediaID networkid.MediaID, params map[string]string) (mediaproxy.GetMediaResponse, error) {
	parsedID, err := ParseMediaID(mediaID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse media ID: %w", err)
	}

	switch parsedID.Type {
	case mediaIDTypeAvatar:
		if parsedID.AvatarURL == "" {
			return nil, fmt.Errorf("empty avatar URL in media ID")
		}
		return &mediaproxy.GetMediaResponseURL{URL: parsedID.AvatarURL}, nil

	case mediaIDTypeAttachment:
		if parsedID.AttachmentURL == "" {
			return nil, fmt.Errorf("empty attachment URL in media ID")
		}
		ul := bc.Bridge.GetCachedUserLoginByID(parsedID.UserLoginID)
		if ul == nil {
			return nil, fmt.Errorf("user login not found")
		}
		client := ul.Client.(*BookfaceClient)
		return client.fetchAttachment(ctx, parsedID.AttachmentURL)

	default:
		return nil, fmt.Errorf("unknown media ID type %d", parsedID.Type)
	}
}

// fetchAttachment fetches an attachment by getting a signed S3 URL from bookface.
func (bc *BookfaceClient) fetchAttachment(ctx context.Context, mediaURL string) (mediaproxy.GetMediaResponse, error) {
	log := zerolog.Ctx(ctx)

	httpClient := &http.Client{
		Jar: bc.Client.GetCookieJar(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", bookface.UserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		return nil, fmt.Errorf("expected 302 redirect, got status %d", resp.StatusCode)
	}

	signedURL := resp.Header.Get("Location")
	if signedURL == "" {
		return nil, fmt.Errorf("no Location header in redirect response")
	}

	// Check if we got redirected to login page instead of S3
	if strings.HasPrefix(signedURL, "https://bookface.ycombinator.com/") && !strings.Contains(signedURL, "amazonaws.com") {
		log.Error().Str("redirect_url", signedURL).Msg("Auth failed - redirected to login page instead of S3")
		return nil, fmt.Errorf("authentication failed: redirected to login page")
	}

	return &mediaproxy.GetMediaResponseURL{URL: signedURL}, nil
}
