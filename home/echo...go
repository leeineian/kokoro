package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/leeineian/minder/sys"
)

func init() {
	adminPerm := discord.PermissionAdministrator

	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "echo",
		Description:              "Echo a message back to you",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{
				Name:        "message",
				Description: "Message to echo",
				Required:    true,
			},
			discord.ApplicationCommandOptionBool{
				Name:        "ephemeral",
				Description: "Whether the message should be ephemeral (default: true)",
				Required:    false,
			},
		},
	}, handleEcho)
}

func handleEcho(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	message := data.String("message")
	ephemeral := false
	if eph, ok := data.OptBool("ephemeral"); ok {
		ephemeral = eph
	}

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(message),
			),
		).
		SetEphemeral(ephemeral).
		Build())
}
