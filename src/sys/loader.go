package sys

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

var commands = []*discordgo.ApplicationCommand{}
var commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
var autocompleteHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
var componentHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
var interactionCallbacks = []func(){}

// CreateSession creates and opens a Discord session with all required intents and handlers configured.
func CreateSession(token string) (*discordgo.Session, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}

	s.AddHandler(InteractionHandler)

	s.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMembers |
		discordgo.IntentsGuildPresences |
		discordgo.IntentMessageContent |
		discordgo.IntentsGuildMessageReactions

	if err := s.Open(); err != nil {
		return nil, err
	}

	return s, nil
}

func RegisterInteractionCallback(f func()) {
	interactionCallbacks = append(interactionCallbacks, f)
}

func RegisterCommand(cmd *discordgo.ApplicationCommand, handler func(s *discordgo.Session, i *discordgo.InteractionCreate)) {
	commands = append(commands, cmd)
	commandHandlers[cmd.Name] = handler
}

func RegisterAutocompleteHandler(cmdName string, handler func(s *discordgo.Session, i *discordgo.InteractionCreate)) {
	autocompleteHandlers[cmdName] = handler
}

func RegisterComponentHandler(customID string, handler func(s *discordgo.Session, i *discordgo.InteractionCreate)) {
	componentHandlers[customID] = handler
}

func RegisterCommands(s *discordgo.Session, guildID string) error {
	LogInfo("Registering commands...")

	// If a guild ID is provided, register commands to that guild AND clear global commands.
	if guildID != "" {
		LogInfo("Registering commands to guild: %s", guildID)
		createdCommands, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, guildID, commands)
		if err != nil {
			return fmt.Errorf("[ERROR] Failed to register guild commands: %w", err)
		}
		for _, cmd := range createdCommands {
			LogInfo("Registered guild command: %s", cmd.Name)
		}

		// Clear global commands to remove old ones
		LogInfo("Clearing global commands...")
		_, err = s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", []*discordgo.ApplicationCommand{})
		if err != nil {
			LogWarn("Failed to clear global commands: %v", err)
		} else {
			LogInfo("Global commands cleared.")
		}

		return nil
	}

	// Otherwise, register commands globally
	LogInfo("Registering commands globally...")
	createdCommands, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", commands)
	if err != nil {
		return fmt.Errorf("[ERROR] Failed to register global commands: %w", err)
	}
	for _, cmd := range createdCommands {
		LogInfo("Registered global command: %s", cmd.Name)
	}
	return nil
}

func InteractionHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	for _, f := range interactionCallbacks {
		go f()
	}
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			go h(s, i)
		}
	case discordgo.InteractionApplicationCommandAutocomplete:
		if h, ok := autocompleteHandlers[i.ApplicationCommandData().Name]; ok {
			go h(s, i)
		}
	case discordgo.InteractionMessageComponent:
		if h, ok := componentHandlers[i.MessageComponentData().CustomID]; ok {
			go h(s, i)
		}
	}
}
