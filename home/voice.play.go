package home

import (
	"context"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

func init() {
	// Replaced by autocomplete
}

func handleMusicPlay(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	query, _ := data.OptString("query")
	mode, _ := data.OptString("queue")
	now := mode == "now"

	// Instant Defer
	_ = event.DeferCreateMessage(false)

	if err := startPlayback(event, query, now); err != nil {
		sys.LogError("Playback error: %v", err)
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
			SetContent("Failed to start player: "+err.Error()).
			Build())
	}
}

func handleMusicAutocomplete(event *events.AutocompleteInteractionCreate) {
	data := event.Data
	focused := data.Focused()
	if focused.Name != "query" {
		return
	}
	query := focused.String()
	if query == "" {
		_ = event.AutocompleteResult(nil)
		return
	}

	vm := proc.GetVoiceManager()
	// Quick search for autocomplete (don't need deep discovery here)
	results, err := vm.Search(query)
	if err != nil {
		_ = event.AutocompleteResult(nil)
		return
	}

	var choices []discord.AutocompleteChoice
	for i, r := range results {
		if i >= 25 {
			break
		}
		name := r.Title
		if len(name) > 100 {
			name = name[:97] + "..."
		}

		// Use URL as value for instant playback
		val := r.URL
		if len(val) > 100 {
			val = r.Title // Fallback to title if URL is too long
			if len(val) > 100 {
				val = val[:100]
			}
		}

		choices = append(choices, discord.AutocompleteChoiceString{
			Name:  name,
			Value: val,
		})
	}
	_ = event.AutocompleteResult(choices)
}

func startPlayback(event *events.ApplicationCommandInteractionCreate, query string, now bool) error {
	// 2. Get User's Voice State
	var voiceState discord.VoiceState
	var ok bool
	if event.Member() != nil {
		voiceState, ok = event.Client().Caches.VoiceState(*event.GuildID(), event.User().ID)
	}

	if !ok || voiceState.ChannelID == nil {
		return context.Canceled // Or any error indicating they need to be in VC
	}

	// 4. Join & Play (Concurrent Optimization)
	vm := proc.GetVoiceManager()

	// CRITICAL: Prepare the session structure synchronously first.
	// This ensures vs.GetSession() will succeed in parallel calls.
	_ = vm.Prepare(event.Client(), *event.GuildID(), *voiceState.ChannelID)

	joinErr := make(chan error, 1)
	go func() {
		joinErr <- vm.Join(context.Background(), event.Client(), *event.GuildID(), *voiceState.ChannelID)
	}()

	// Start resolution while joining (Safe now because session is Prepared)
	track, err := vm.Play(context.Background(), *event.GuildID(), query, now)
	if err != nil {
		return err
	}

	// Ensure Join finished before we try to stream (though Play doesn't stream immediately)
	if err := <-joinErr; err != nil {
		return err
	}

	// 6. Wait for metadata to show the title and link
	_ = track.Wait()
	return finishPlaybackResponse(event, track, now)
}

func finishPlaybackResponse(event *events.ApplicationCommandInteractionCreate, track *proc.Track, now bool) error {
	// 7. Response
	prefix := "🎶 Playing:"
	if !now {
		prefix = "✅ Added to queue:"
	}

	_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
		SetContent(prefix+" ["+track.Title+"]("+track.URL+")").
		Build())
	return nil
}
