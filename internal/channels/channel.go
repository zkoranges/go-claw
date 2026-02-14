package channels

import (
	"context"
)

// Channel defines the interface for a messaging platform integration.
type Channel interface {
	// Name returns the unique name of the channel (e.g., "telegram").
	Name() string

	// Start begins listening for messages. It should block until the context is canceled or a fatal error occurs.
	Start(ctx context.Context) error
}
