package home

import (
	"os"
	"syscall"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleSessionReboot(event *events.ApplicationCommandInteractionCreate) {
	sys.LogWarn("Reboot commanded by user %s (%s)", event.User().Username, event.User().ID)
	sys.RestartRequested = true

	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay("🔄 **Rebooting...**"),
			),
		).
		SetEphemeral(true).
		Build())

	// Give a small delay to ensure the message is dispatched to Discord
	time.Sleep(1 * time.Second)

	// Send SIGTERM to ourselves to trigger graceful shutdown logic in main.go
	// The supervisor (Docker/systemd) will then restart the process.
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
}
