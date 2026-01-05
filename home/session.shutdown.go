package home

import (
	"os"
	"syscall"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleSessionShutdown(event *events.ApplicationCommandInteractionCreate) {
	sys.LogWarn("Shutdown commanded by user %s (%s)", event.User().Username, event.User().ID)

	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay("🛑 **Shutting down...**"),
			),
		).
		SetEphemeral(true).
		Build())

	// Give a small delay to ensure the message is dispatched to Discord
	time.Sleep(1 * time.Second)

	// Send SIGTERM to ourselves to trigger graceful shutdown logic in main.go
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
}
