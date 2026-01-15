package home

import (
	"context"
	"strconv"
	"strings"

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
	queueVal, _ := data.OptString("queue")
	autoplay, hasAutoplay := data.OptBool("autoplay")
	loop, hasLoop := data.OptBool("loop")

	mode := ""
	position := 0
	if queueVal == "now" {
		mode = "now"
	} else if queueVal == "next" {
		mode = "next"
	} else if queueVal != "" {
		if pos, err := strconv.Atoi(queueVal); err == nil {
			position = pos
		}
	}

	// Instant Defer
	_ = event.DeferCreateMessage(false)

	if err := startPlayback(event, query, mode, autoplay, hasAutoplay, loop, hasLoop, position); err != nil {
		sys.LogError("Playback error: %v", err)
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
			SetContent("Failed to start player: "+err.Error()).
			Build())
	}
}

func handleMusicAutocomplete(event *events.AutocompleteInteractionCreate) {
	data := event.Data
	focused := data.Focused()

	if focused.Name == "queue" {
		val := focused.String()
		choices := []discord.AutocompleteChoice{
			discord.AutocompleteChoiceString{Name: "Play Now", Value: "now"},
			discord.AutocompleteChoiceString{Name: "Play Next", Value: "next"},
		}
		if val != "" {
			if _, err := strconv.Atoi(val); err == nil {
				choices = append([]discord.AutocompleteChoice{
					discord.AutocompleteChoiceString{Name: "Position: " + val, Value: val},
				}, choices...)
			}
		}
		_ = event.AutocompleteResult(choices)
		return
	}

	if focused.Name != "query" {
		return
	}
	query := focused.String()
	if query == "" {
		_ = event.AutocompleteResult(nil)
		return
	}

	vm := proc.GetVoiceManager()

	// If the user pasted a link, don't trigger a search.
	if strings.Contains(query, "http://") || strings.Contains(query, "https://") {
		_ = event.AutocompleteResult(nil)
		return
	}

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

func startPlayback(event *events.ApplicationCommandInteractionCreate, query string, mode string, autoplay bool, hasAutoplay bool, loop bool, hasLoop bool, position int) error {
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
	sess := vm.Prepare(event.Client(), *event.GuildID(), *voiceState.ChannelID)

	// Update options from command (no longer persistent across different play commands)
	sess.Autoplay = autoplay
	sess.Looping = loop

	joinErr := make(chan error, 1)
	go func() {
		joinErr <- vm.Join(context.Background(), event.Client(), *event.GuildID(), *voiceState.ChannelID)
	}()

	// Start resolution while joining (Safe now because session is Prepared)
	track, err := vm.Play(context.Background(), *event.GuildID(), query, mode, position)
	if err != nil {
		return err
	}

	// Ensure Join finished before we try to stream (though Play doesn't stream immediately)
	if err := <-joinErr; err != nil {
		return err
	}

	// 6. Wait for metadata to show the title and link
	_ = track.Wait()
	return finishPlaybackResponse(event, track, mode, sess.Autoplay, sess.Looping, position)
}

func finishPlaybackResponse(event *events.ApplicationCommandInteractionCreate, track *proc.Track, mode string, autoplay bool, looping bool, position int) error {
	// 7. Response
	var prefix string
	if mode == "next" {
		prefix = "⏭️ Next up:"
	} else if mode == "now" {
		prefix = "🎶 Playing now:"
	} else if position > 0 {
		prefix = "✅ Added to queue at position " + strconv.Itoa(position) + ":"
	} else if mode != "" {
		prefix = "✅ Added to queue:"
	} else {
		prefix = "🎶 Playing:"
	}

	var status []string
	if autoplay {
		status = append(status, "Autoplay")
	}
	if looping {
		status = append(status, "Looping")
	}

	statusStr := ""
	if len(status) > 0 {
		statusStr = " (" + strings.Join(status, ", ") + ": Enabled)"
	}

	content := prefix + " [" + track.Title + "](" + track.URL + ")"
	if track.Channel != "" {
		content += " · " + track.Channel
	}
	content += statusStr

	_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
		SetContent(content).
		Build())
	return nil
}
