package home

import (
	"fmt"
	"sort"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/leeineian/minder/sys"
)

func init() {
	adminPerm := discord.PermissionAdministrator

	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "test",
		Description:              "Testing and Preview Utilities (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "error",
				Description: "Preview user-facing error constants",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "key",
						Description:  "The error constant to preview",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
		},
	}, handleTest)

	sys.RegisterAutocompleteHandler("test", handleTestAutocomplete)
}

func handleTest(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	subCmd := *data.SubCommandName

	switch subCmd {
	case "error":
		handleTestError(event, data)
	}
}

func handleTestError(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	key := data.String("key")

	errors := sys.GetUserErrors()
	var content string
	if msg, ok := errors[key]; ok {
		content = fmt.Sprintf("**%s**\n\n%s", key, msg)
	} else {
		content = fmt.Sprintf("❌ Error constant `%s` not found.", key)
	}

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
			),
		).
		SetEphemeral(true).
		Build())
}

func handleTestAutocomplete(event *events.AutocompleteInteractionCreate) {
	data := event.Data

	var choices []discord.AutocompleteChoice

	// Find focused option
	var focusedName string
	var focusedValue string

	for _, opt := range data.Options {
		if opt.Focused {
			focusedName = opt.Name
			if opt.Value != nil {
				focusedValue = strings.Trim(string(opt.Value), `"`)
			}
			break
		}
	}

	if focusedName == "key" {
		errors := sys.GetUserErrors()
		keys := make([]string, 0, len(errors))
		for k := range errors {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			if focusedValue == "" || strings.Contains(strings.ToLower(k), strings.ToLower(focusedValue)) {
				displayName := k + ": " + errors[k]
				if len(displayName) > 100 {
					displayName = displayName[:97] + "..."
				}
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  displayName,
					Value: k,
				})
				if len(choices) >= 25 {
					break
				}
			}
		}
	}

	event.AutocompleteResult(choices)
}
