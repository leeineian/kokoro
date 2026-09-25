package golibdave

import (
	"log/slog"

	"github.com/disgoorg/godave"
)

// NewSession creates a new pure-Go godave session.
func NewSession(logger *slog.Logger, userID godave.UserID, callbacks godave.Callbacks) godave.Session {
	return godave.NewSession(logger, userID, callbacks)
}
