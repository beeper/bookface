package connector

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/mediaproxy"

	"github.com/beeper/bookface/pkg/bookface"
)

var _ bridgev2.DirectMediableNetwork = (*BookfaceConnector)(nil)

// SetUseDirectMedia is called when direct media is enabled.
// It sets a flag so that avatar generation uses direct media URIs.
func (bc *BookfaceConnector) SetUseDirectMedia() {
	bc.DirectMedia = true
}

// Download handles fetching media when a Matrix client requests a direct media URL.
// Avatar URLs (S3) are publicly accessible and can be redirected.
// Attachment URLs (bookface.ycombinator.com/media) require authentication and must be fetched.
func (bc *BookfaceConnector) Download(ctx context.Context, mediaID networkid.MediaID, params map[string]string) (mediaproxy.GetMediaResponse, error) {
	log := zerolog.Ctx(ctx)
	log.Debug().Int("media_id_len", len(mediaID)).Msg("Download called")

	parsedID, err := ParseMediaID(mediaID)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to parse media ID")
		return nil, fmt.Errorf("failed to parse media ID: %w", err)
	}

	switch parsedID.Type {
	case mediaIDTypeAvatar:
		if parsedID.AvatarURL == "" {
			return nil, fmt.Errorf("empty avatar URL in media ID")
		}
		log.Debug().Str("url", parsedID.AvatarURL).Msg("Serving avatar redirect")
		// Avatar URLs are public S3 URLs, can redirect directly
		return &mediaproxy.GetMediaResponseURL{
			URL: parsedID.AvatarURL,
		}, nil

	case mediaIDTypeAttachment:
		if parsedID.AttachmentURL == "" {
			return nil, fmt.Errorf("empty attachment URL in media ID")
		}
		log.Debug().Str("url", parsedID.AttachmentURL).Msg("Fetching attachment with auth")
		// Attachment URLs require authentication, must fetch through bridge
		return bc.fetchAttachmentWithAuth(ctx, parsedID.AttachmentURL)

	default:
		return nil, fmt.Errorf("unknown media ID type %d", parsedID.Type)
	}
}

// fetchAttachmentWithAuth fetches an attachment by getting a signed S3 URL from bookface.
// The /media endpoint returns a 302 redirect to a time-limited signed S3 URL.
// This uses the shared cookie jar which automatically handles Set-Cookie responses.
func (bc *BookfaceConnector) fetchAttachmentWithAuth(ctx context.Context, mediaURL string) (mediaproxy.GetMediaResponse, error) {
	log := zerolog.Ctx(ctx)

	jar := bc.GetCookieJar()
	if jar == nil {
		return nil, fmt.Errorf("no cookie jar available for authenticated fetch")
	}

	// Log cookie jar contents for debugging
	// Media URLs use bookface.ycombinator.com, not messages.ycombinator.com
	parsedURL, _ := url.Parse(bookface.BookfaceURL)
	cookies := jar.Cookies(parsedURL)
	var cookieNames []string
	for _, c := range cookies {
		cookieNames = append(cookieNames, c.Name)
	}
	log.Debug().
		Strs("cookie_names", cookieNames).
		Int("cookie_count", len(cookies)).
		Str("domain", parsedURL.Host).
		Msg("Cookie jar contents for media fetch")

	// Create a client that doesn't follow redirects but uses the shared cookie jar.
	// The jar automatically sends cookies and captures Set-Cookie responses.
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", bookface.UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	resp.Body.Close()

	// Note: The cookie jar automatically handled any Set-Cookie headers in the response

	// Expect a 302 redirect to a signed S3 URL
	if resp.StatusCode != http.StatusFound {
		log.Warn().Int("status", resp.StatusCode).Str("url", mediaURL).Msg("Unexpected status from media endpoint")
		return nil, fmt.Errorf("expected 302 redirect, got status %d", resp.StatusCode)
	}

	signedURL := resp.Header.Get("Location")
	if signedURL == "" {
		return nil, fmt.Errorf("no Location header in redirect response")
	}

	// Check if we got redirected to login page instead of S3
	if strings.HasPrefix(signedURL, "https://bookface.ycombinator.com/") && !strings.Contains(signedURL, "amazonaws.com") {
		log.Error().
			Str("redirect_url", signedURL).
			Strs("cookie_names", cookieNames).
			Msg("Auth failed - redirected to login page instead of S3")
		return nil, fmt.Errorf("authentication failed: redirected to login page")
	}

	log.Debug().
		Str("signed_url", signedURL).
		Bool("is_s3", strings.Contains(signedURL, "amazonaws.com")).
		Msg("Got signed S3 URL for attachment")

	// Return redirect to the signed S3 URL (expires in 60 seconds, but client fetches immediately)
	return &mediaproxy.GetMediaResponseURL{
		URL: signedURL,
	}, nil
}
