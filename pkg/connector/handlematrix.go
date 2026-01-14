package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
)

// HandleMatrixMessage handles outgoing messages from Matrix to Bookface.
func (bc *BookfaceClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	// TODO: Implement sending messages to Bookface
	return nil, fmt.Errorf("sending messages is not yet implemented")
}
