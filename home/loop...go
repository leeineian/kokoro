package home

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

func init() {
	adminPerm := discord.PermissionAdministrator

	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "loop",
		Description:              "Webhook stress testing and looping utilities (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "erase",
				Description: "Erase a configured loop category",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "target",
						Description:  "Target configuration to erase",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "set",
				Description: "Configure a category for looping",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "category",
						Description:  "Category to configure",
						Required:     true,
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionString{
						Name:        "message",
						Description: "Message to send (default: @everyone)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "webhook_author",
						Description: "Webhook display name (default: LoopHook)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "webhook_avatar",
						Description: "Webhook avatar URL",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "thread_message",
						Description: "Message for threads (default: disabled)",
						Required:    false,
					},
					discord.ApplicationCommandOptionInt{
						Name:        "thread_count",
						Description: "Amount of threads per channel (default: disabled)",
						Required:    false,
					},
					discord.ApplicationCommandOptionChannel{
						Name:        "vote_channel",
						Description: "Channel where the vote panel will be posted",
						Required:    false,
					},
					discord.ApplicationCommandOptionRole{
						Name:        "vote_role",
						Description: "Role required to vote (and for % calculation)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "vote_message",
						Description: "Custom message to display on the vote panel",
						Required:    false,
					},
					discord.ApplicationCommandOptionInt{
						Name:        "vote_threshold",
						Description: "Percentage of role members required to resume (1-100)",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "start",
				Description: "Start webhook loop(s)",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "target",
						Description:  "Target to start (all or specific channel)",
						Required:     true,
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionString{
						Name:        "duration",
						Description: "Duration to run (e.g., 30s, 5m, 1h). Leave empty for random mode.",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stop",
				Description: "Stop webhook loop(s)",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "target",
						Description:  "Target to stop (all or specific channel)",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: "View all current loop configurations and their status",
			},
		},
	}, handleLoop)

	sys.RegisterAutocompleteHandler("loop", handleLoopAutocomplete)
}

func handleLoop(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	subCmd := *data.SubCommandName
	switch subCmd {
	case "stats":
		handleLoopStats(event)
	case "erase":
		handleLoopErase(event)
	case "set":
		handleLoopSet(event, data)
	case "start":
		handleLoopStart(event, data)
	case "stop":
		handleLoopStop(event, data)
	default:
		log.Printf("Unknown loop subcommand: %s", subCmd)
	}
}

func loopRespond(event *events.ApplicationCommandInteractionCreate, content string, ephemeral bool) {
	// Add some spacing/formatting to make it look cleaner
	var displayContent string
	if !strings.HasPrefix(content, "#") && !strings.HasPrefix(content, ">") {
		displayContent = "> " + content
	} else {
		displayContent = content
	}

	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(displayContent),
			),
		).
		SetEphemeral(ephemeral)

	// Try to create a message. If it fails (likely due to defer), try updating the original response.
	err := event.CreateMessage(builder.Build())
	if err != nil {
		updateBuilder := discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(displayContent),
				),
			)
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), updateBuilder.Build())
	}
}

func handleLoopAutocomplete(event *events.AutocompleteInteractionCreate) {
	data := event.Data
	focusedOpt := ""
	for _, opt := range data.Options {
		if opt.Focused {
			if opt.Value != nil {
				focusedOpt = strings.Trim(string(opt.Value), `"`)
			}
			break
		}
	}

	subCmd := ""
	if data.SubCommandName != nil {
		subCmd = *data.SubCommandName
	}

	handleWebhookLooperAutocomplete(event, subCmd, focusedOpt)
}

func getLoopStatusDetails(cfg *sys.LoopConfig, state *proc.LoopState) (string, string) {
	if state == nil {
		return sys.MsgLoopStatusStopped, ""
	}
	emoji := sys.MsgLoopStatusRunning
	details := ""
	if cfg.Interval > 0 {
		details += fmt.Sprintf(sys.MsgLoopStatusRound, state.CurrentRound)
	} else {
		details += fmt.Sprintf(sys.MsgLoopStatusRoundBatch, state.CurrentRound, state.RoundsTotal)
	}

	if !state.NextRun.IsZero() {
		details += fmt.Sprintf(sys.MsgLoopStatusNextRun, state.NextRun.Format(sys.DefaultTimeFormat))
	} else if !state.EndTime.IsZero() {
		if state.EndTime.After(time.Now().UTC()) {
			details += fmt.Sprintf(sys.MsgLoopStatusEnds, state.EndTime.Format(sys.DefaultTimeFormat))
		} else {
			details += sys.MsgLoopStatusFinishing
		}
	}
	return emoji, details
}

// handleWebhookLooperAutocomplete provides autocomplete for webhook looper commands
func handleWebhookLooperAutocomplete(event *events.AutocompleteInteractionCreate, subCmd string, focusedOpt string) {
	var choices []discord.AutocompleteChoice

	switch subCmd {
	case "start":
		configs, _ := sys.GetAllLoopConfigs(sys.AppContext)
		activeLoops := proc.GetActiveLoops()

		// Add "all" option if there are multiple configs and it matches the filter
		if len(configs) > 1 {
			if focusedOpt == "" || strings.Contains(sys.MsgLoopSearchStartAll, strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  sys.MsgLoopChoiceStartAll,
					Value: "all",
				})
			}
		}

		for _, data := range configs {
			// Only show configs for the current guild
			if ch, ok := event.Client().Caches.Channel(data.ChannelID); ok {
				if ch.GuildID() != *event.GuildID() {
					continue
				}
			} else {
				continue
			}

			// Try to get latest name from cache
			displayName := data.ChannelName
			if ch, ok := event.Client().Caches.Channel(data.ChannelID); ok {
				displayName = ch.Name()
			}

			intervalStr := proc.FormatDuration(proc.IntervalMsToDuration(data.Interval))
			emoji, details := getLoopStatusDetails(data, activeLoops[data.ChannelID])

			if focusedOpt == "" || strings.Contains(strings.ToLower(displayName), strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  fmt.Sprintf(sys.MsgLoopChoiceStart, displayName, emoji, details, intervalStr),
					Value: data.ChannelID.String(),
				})
			}
		}

	case "set":
		// Only show categories in the current guild
		guildID := *event.GuildID()
		for ch := range event.Client().Caches.Channels() {
			if ch.GuildID() == guildID && ch.Type() == discord.ChannelTypeGuildCategory {
				if focusedOpt == "" || strings.Contains(strings.ToLower(ch.Name()), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{
						Name:  fmt.Sprintf(sys.MsgLoopChoiceCategory, ch.Name()),
						Value: ch.ID().String(),
					})
				}
			}
		}

	case "erase":
		configs, _ := sys.GetAllLoopConfigs(sys.AppContext)
		guildID := *event.GuildID()

		// Add "all" option if there are multiple configs and it matches the filter
		if len(configs) > 1 {
			if focusedOpt == "" || strings.Contains(sys.MsgLoopSearchEraseAll, strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  sys.MsgLoopChoiceEraseAll,
					Value: "all",
				})
			}
		}

		activeLoops := proc.GetActiveLoops()

		for _, cfg := range configs {
			// Check if the loop belongs to the current guild
			if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
				if ch.GuildID() != guildID {
					continue
				}
			} else {
				continue
			}

			displayName := cfg.ChannelName
			if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
				displayName = ch.Name()
			}

			intervalStr := proc.FormatDuration(proc.IntervalMsToDuration(cfg.Interval))
			emoji, details := getLoopStatusDetails(cfg, activeLoops[cfg.ChannelID])

			if focusedOpt == "" || strings.Contains(strings.ToLower(displayName), strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  fmt.Sprintf(sys.MsgLoopChoiceErase, displayName, emoji, details, intervalStr),
					Value: cfg.ChannelID.String(),
				})
			}
		}

	case "stop":
		activeLoops := proc.GetActiveLoops()
		configs, _ := sys.GetAllLoopConfigs(sys.AppContext)
		guildID := *event.GuildID()

		// Add "all" option if there are multiple running loops and it matches the filter
		if len(activeLoops) > 1 {
			if focusedOpt == "" || strings.Contains(sys.MsgLoopSearchStopAll, strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  sys.MsgLoopChoiceStopAll,
					Value: "all",
				})
			}
		}

		for channelID, state := range activeLoops {
			// Check if the loop belongs to the current guild
			var belongsToGuild bool
			if ch, ok := event.Client().Caches.Channel(channelID); ok {
				if ch.GuildID() == guildID {
					belongsToGuild = true
				}
			}
			if !belongsToGuild {
				continue
			}

			var config *sys.LoopConfig
			name := channelID.String()
			for _, cfg := range configs {
				if cfg.ChannelID == channelID {
					config = cfg
					name = cfg.ChannelName
					// Try to get latest name from cache
					if ch, ok := event.Client().Caches.Channel(channelID); ok {
						name = ch.Name()
					}
					break
				}
			}

			if config == nil {
				continue
			}

			intervalStr := proc.FormatDuration(proc.IntervalMsToDuration(config.Interval))
			emoji, details := getLoopStatusDetails(config, state)

			// Filter by channel name
			if focusedOpt == "" || strings.Contains(strings.ToLower(name), strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  fmt.Sprintf(sys.MsgLoopChoiceStop, name, emoji, details, intervalStr),
					Value: channelID.String(),
				})
			}
		}
	}

	// Limit to 25
	if len(choices) > 25 {
		choices = choices[:25]
	}

	event.AutocompleteResult(choices)
}
