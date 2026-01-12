package home

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleSessionReboot(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	build, _ := data.OptBool("build")

	sys.LogWarn(sys.MsgSessionRebootCommanded, event.User().Username, event.User().ID)

	// Immediate response
	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(sys.MsgSessionRebooting),
			),
		).
		SetEphemeral(true).
		Build())

	if build {
		// Update message to show build status
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(),
			discord.NewMessageUpdateBuilder().
				AddComponents(discord.NewContainer(discord.NewTextDisplay(sys.MsgSessionRebootBuilding))).
				Build())

		// Determine current executable path
		exePath, err := os.Executable()
		if err != nil {
			exePath = sys.GetProjectName()
		}

		// Run build command targeting the current executable
		cmd := exec.Command("go", "build", "-o", exePath, ".")
		output, err := cmd.CombinedOutput()
		if err != nil {
			// Show build failure
			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(),
				discord.NewMessageUpdateBuilder().
					AddComponents(discord.NewContainer(discord.NewTextDisplay(fmt.Sprintf(sys.MsgSessionRebootBuildFail, string(output))))).
					Build())
			return
		}

		// Show build success
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(),
			discord.NewMessageUpdateBuilder().
				AddComponents(discord.NewContainer(discord.NewTextDisplay(sys.MsgSessionRebootBuildSuccess+"\n"+sys.MsgSessionRebooting))).
				Build())
	}

	sys.RestartRequested = true

	// Give a small delay to ensure the message is dispatched to Discord
	time.Sleep(1500 * time.Millisecond)

	// Send SIGTERM to ourselves to trigger graceful shutdown logic in main.go
	// The supervisor (Docker/systemd) will then restart the (newly built) process.
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
}
