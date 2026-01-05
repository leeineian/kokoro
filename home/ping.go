package home

import (
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/sys"
)

func init() {
	adminPerm := discord.PermissionAdministrator

	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "ping",
		Description:              "Check bot latency (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionBool{
				Name:        "ephemeral",
				Description: "Whether the message should be ephemeral (default: true)",
				Required:    false,
			},
		},
	}, handlePing)

	sys.RegisterComponentHandler("ping_refresh", handlePingRefresh)
}

func handlePing(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	ephemeral := true
	if eph, ok := data.OptBool("ephemeral"); ok {
		ephemeral = eph
	}

	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		SetEphemeral(ephemeral).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay("🏓 Pinging..."),
			),
		)

	err := event.CreateMessage(builder.Build())
	if err != nil {
		sys.LogDebug("Failed to send ping: %v", err)
		return
	}

	go func() {
		interTime := snowflake.ID(event.ID()).Time()
		latency := time.Since(interTime).Milliseconds()

		content := fmt.Sprintf("# Pong! 🏓\n\n> **Latency:** %dms", latency)

		updateBuilder := discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true)

		updateBuilder.AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
				discord.NewActionRow(
					discord.NewSuccessButton("🔄 Refresh", "ping_refresh"),
				),
			),
		)

		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), updateBuilder.Build())
	}()
}

func handlePingRefresh(event *events.ComponentInteractionCreate) {
	interTime := snowflake.ID(event.ID()).Time()
	latency := time.Since(interTime).Milliseconds()

	content := fmt.Sprintf("# Pong! 🔁\n\n> **Latency:** %dms", latency)

	updateBuilder := discord.NewMessageUpdateBuilder().
		SetIsComponentsV2(true)

	updateBuilder.AddComponents(
		discord.NewContainer(
			discord.NewTextDisplay(content),
			discord.NewActionRow(
				discord.NewSuccessButton("🔄 Refresh", "ping_refresh"),
			),
		),
	)

	_ = event.UpdateMessage(updateBuilder.Build())
}
