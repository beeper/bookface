package connector

import (
	"context"

	"github.com/beeper/bookface/pkg/bookface"
)

// StartWebSocket initializes the ActionCable connection for real-time updates.
func (bc *BookfaceClient) StartWebSocket(ctx context.Context) error {
	metadata := bc.UserLogin.Metadata.(*UserLoginMetadata)

	bc.actionCable = bookface.NewActionCable(
		metadata.Cookies,
		metadata.UserID,
		bc.handleActionCableEvent,
		bc.UserLogin.Log,
	)

	return bc.actionCable.Start(ctx)
}

// StopWebSocket gracefully closes the ActionCable connection.
func (bc *BookfaceClient) StopWebSocket() {
	if bc.actionCable != nil {
		bc.actionCable.Stop()
		bc.actionCable = nil
	}
}
