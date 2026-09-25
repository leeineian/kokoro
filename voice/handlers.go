package voice

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

func handleVoice(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}
	switch *data.SubCommandName {
	case "play":
		handleMusicPlay(event, data)
	case "stop":
		handleMusicStop(event, data)
	case "queue":
		handleMusicQueue(event, data)
	case "forward":
		handleMusicSeek(event, data, 1)
	case "rewind":
		handleMusicSeek(event, data, -1)
	case "skip":
		handleMusicSkip(event)
	case "volume":
		handleVoiceVolume(event, data)
	case "panel":
		handleVoicePanel(event)
	}
}

func handleVoiceVolume(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	s, ok := mustGetSession(event)
	if !ok {
		return
	}
	vol := data.Int("set")
	s.Volume.Store(int32(vol))
	UpdateVoicePanels(*event.GuildID(), *event.Client())
	_ = voiceSys.RespondInteractionV2(*event.Client(), event, fmt.Sprintf("Volume set to **%d%%**.", vol), false)
}

func handleMusicSeek(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData, factor int) {
	s, ok := mustGetSession(event)
	if !ok {
		return
	}
	dStr := data.String("duration")
	d, err := time.ParseDuration(dStr)
	if err != nil {
		_ = voiceSys.RespondInteractionV2(*event.Client(), event, "Invalid duration format (use 10s, 1m etc).", true)
		return
	}

	seekDuration := d
	if factor < 0 {
		seekDuration = -d
	}
	if err := s.Seek(seekDuration); err != nil {
		_ = voiceSys.RespondInteractionV2(*event.Client(), event, fmt.Sprintf("Seek failed: %v", err), false)
		return
	}
	action := "Forwarded"
	if factor < 0 {
		action = "Rewound"
	}
	UpdateVoicePanels(*event.GuildID(), *event.Client())
	_ = voiceSys.RespondInteractionV2(*event.Client(), event, fmt.Sprintf("%s %v", action, d), false)
}

func handleMusicSkip(event *events.ApplicationCommandInteractionCreate) {
	s, ok := mustGetSession(event)
	if !ok {
		return
	}
	_ = event.DeferCreateMessage(false)

	title, err := s.Skip()
	if err != nil {
		_ = voiceSys.EditInteractionV2(*event.Client(), event, fmt.Sprintf("Failed to skip: %v", err))
		return
	}
	UpdateVoicePanels(*event.GuildID(), *event.Client())
	_ = voiceSys.EditInteractionV2(*event.Client(), event, fmt.Sprintf("Skipped: %s", title))
}

func handleMusicPlay(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	q, m, p, a, l := parsePlayArguments(data)

	if _, ok := mustGetUserVoiceState(event); !ok {
		return
	}

	_ = event.DeferCreateMessage(false)
	if strings.HasPrefix(strings.ToUpper(q), "[PL]") {
		qBody := strings.TrimSpace(q[4:])
		if qBody != "" && !strings.Contains(qBody, "http") {
			rs, err := GetVoiceManager().SearchPlaylist(qBody)
			if err == nil && len(rs) > 0 {
				q = rs[0].URL
			}
		}
	}

	if err := startPlayback(event, q, m, a, l, p); err != nil {
		_ = voiceSys.EditInteractionV2(*event.Client(), event, "Failed: "+err.Error())
	}
}

func parsePlayArguments(data discord.SlashCommandInteractionData) (q, m string, p int, a, l bool) {
	q, _ = data.OptString("query")
	qv, _ := data.OptString("queue")
	a, _ = data.OptBool("autoplay")
	l, _ = data.OptBool("loop")

	if qv == "now" {
		m = "now"
	} else if qv == "next" {
		m = "next"
	} else if qv != "" {
		p, _ = strconv.Atoi(qv)
	}
	return
}

func handleMusicStop(event *events.ApplicationCommandInteractionCreate, _ discord.SlashCommandInteractionData) {
	voiceSys.LogInfo("User %s (%s) stopped playback in guild %s", event.User().Username, event.User().ID, *event.GuildID())
	GetVoiceManager().Leave(context.Background(), *event.GuildID())
	_ = voiceSys.RespondInteractionV2(*event.Client(), event, "🛑 Stopped and disconnected.", false)
}

func handleMusicQueue(event *events.ApplicationCommandInteractionCreate, _ discord.SlashCommandInteractionData) {
	s, ok := mustGetSession(event)
	if !ok {
		return
	}
	_ = event.DeferCreateMessage(true)

	s.lockQueue()
	defer s.unlockQueue()

	var components []any

	if s.currentTrack != nil {
		components = append(components, s.buildTrackComponents(s.currentTrack, "Now Playing:")...)
		components = append(components, voiceSys.NewSeparator(true))
	}

	components = append(components, voiceSys.NewTextDisplay("**Queue:**"))
	if len(s.queue) == 0 {
		msg := "_Empty_"
		if s.Autoplay && s.autoplayTrack != nil {
			msg = "_Empty (Autoplay Ready)_"
		}
		components = append(components, voiceSys.NewTextDisplay(msg))
	} else {
		var qList strings.Builder
		for i, t := range s.queue {
			if i >= 10 {
				qList.WriteString(fmt.Sprintf("\n*...and %d more*", len(s.queue)-10))
				break
			}
			qList.WriteString(fmt.Sprintf("`%d.` %s\n", i+1, t.FullDisplay()))
		}
		components = append(components, voiceSys.NewTextDisplay(qList.String()))
	}

	if s.Autoplay {
		components = append(components, voiceSys.NewSeparator(true))
		components = append(components, voiceSys.NewTextDisplay("**Autoplay:** Enabled"))
		if s.autoplayTrack != nil {
			components = append(components, s.buildTrackComponents(s.autoplayTrack, "Next Up (Autoplay):")...)
		}
	}

	if err := voiceSys.EditInteractionContainerV2(*event.Client(), event, voiceSys.NewV2Container(components...)); err != nil {
		voiceSys.LogWarn("Failed to edit interaction: %v", err)
	}
}

func handleVoicePanel(event *events.ApplicationCommandInteractionCreate) {
	userID := event.User().ID
	guildID := event.GuildID()
	if guildID == nil {
		_ = voiceSys.RespondInteractionV2(*event.Client(), event, "Not in a guild.", true)
		return
	}

	VoicePanelsMu.Lock()
	if p, ok := VoicePanels[userID]; ok {
		if time.Now().Before(p.ExpiresAt) {
			VoicePanelsMu.Unlock()
			_ = voiceSys.RespondInteractionV2(*event.Client(), event, "You already have an active panel! Dismiss it or wait for it to expire.", true)
			return
		}
		delete(VoicePanels, userID)
	}
	VoicePanelsMu.Unlock()

	s := GetVoiceManager().GetSession(*guildID)
	if s == nil {
		_ = voiceSys.RespondInteractionV2(*event.Client(), event, "Not playing anything.", true)
		return
	}

	_ = event.DeferCreateMessage(true)

	VoicePanelsMu.Lock()
	VoicePanels[userID] = &VoicePanel{
		UserID:    userID,
		GuildID:   *guildID,
		Token:     event.Token(),
		AppID:     event.ApplicationID(),
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	VoicePanelsMu.Unlock()

	UpdateVoicePanels(*guildID, *event.Client())
}

func BuildVoicePanelany(s *VoiceSession) any {
	s.lockQueue()
	defer s.unlockQueue()

	var components []any

	if s.currentTrack != nil {
		components = append(components, s.buildTrackComponents(s.currentTrack, "Now Playing:")...)
	} else {
		components = append(components, voiceSys.NewTextDisplay("**Nothing is currently playing.**"))
	}

	paused := s.IsPaused()
	statusEmoji, statusText := "⏸️", "Playing"
	if paused {
		statusEmoji, statusText = "▶️", "Paused"
	}

	options := ""
	if s.Looping {
		options += " 🔄 Loop"
	}
	if s.Autoplay {
		options += " 🔀 Autoplay"
	}

	components = append(components, voiceSys.NewTextDisplay(fmt.Sprintf("**Status:** %s %s | **Volume:** %d%% %s", statusEmoji, statusText, s.Volume.Load(), options)))
	components = append(components, voiceSys.NewTextDisplay(fmt.Sprintf("**Queue:** %d tracks remaining", len(s.queue))))

	components = append(components, voiceSys.NewSeparator(true))

	playPauseLabel := "⏸️ Pause"
	if paused {
		playPauseLabel = "▶️ Resume"
	}

	row1 := discord.NewActionRow(
		discord.NewButton(discord.ButtonStyleSecondary, playPauseLabel, "voice:panel:playpause", "", 0),
		discord.NewButton(discord.ButtonStyleSecondary, "⏭️ Skip", "voice:panel:skip", "", 0),
		discord.NewButton(discord.ButtonStyleSecondary, "⏹️ Stop", "voice:panel:stop", "", 0),
		discord.NewButton(discord.ButtonStyleDanger, "❌ Close", "voice:panel:close", "", 0),
	)

	row2 := discord.NewActionRow(
		discord.NewButton(discord.ButtonStylePrimary, "🔄 Loop", "voice:panel:loop", "", 0),
		discord.NewButton(discord.ButtonStylePrimary, "🔀 Autoplay", "voice:panel:autoplay", "", 0),
		discord.NewButton(discord.ButtonStyleSecondary, "➖ Vol", "voice:panel:voldown", "", 0),
		discord.NewButton(discord.ButtonStyleSecondary, "➕ Vol", "voice:panel:volup", "", 0),
	)

	components = append(components, row1, row2)

	return voiceSys.NewV2Container(components...)
}

func UpdateVoicePanels(guildID snowflake.ID, cl bot.Client) {
	VoicePanelsMu.Lock()
	defer VoicePanelsMu.Unlock()

	s := GetVoiceManager().GetSession(guildID)

	var container any
	if s == nil {
		container = voiceSys.NewV2Container(voiceSys.NewTextDisplay("The music session has ended."), discord.NewActionRow(discord.NewButton(discord.ButtonStyleDanger, "❌ Close", "voice:panel:close", "", 0)))
	} else {
		s.SetClient(cl)
		container = BuildVoicePanelany(s)
	}

	now := time.Now()
	for userID, panel := range VoicePanels {
		if panel.GuildID != guildID {
			continue
		}
		if now.After(panel.ExpiresAt) {
			delete(VoicePanels, userID)
			continue
		}

		safeGo(func() {
			func(token string, appID snowflake.ID, c any, client bot.Client) {
				_ = voiceSys.EditInteractionContainerV2ByToken(client, appID, token, c)
			}(panel.Token, panel.AppID, container, cl)
		})

		if s == nil {
			delete(VoicePanels, userID)
		}
	}
}

func handleVoiceComponent(event *events.ComponentInteractionCreate) {
	customID := event.Data.CustomID()
	if !strings.HasPrefix(customID, "voice:panel:") {
		return
	}

	action := strings.TrimPrefix(customID, "voice:panel:")
	guildID := event.GuildID()
	if guildID == nil {
		return
	}

	switch action {
	case "close":
		VoicePanelsMu.Lock()
		delete(VoicePanels, event.User().ID)
		VoicePanelsMu.Unlock()
		_ = event.UpdateMessage(discord.NewMessageUpdate().WithContent("Panel closed.").ClearComponents())
		return
	}

	s := GetVoiceManager().GetSession(*guildID)
	if s == nil {
		_ = event.CreateMessage(discord.NewMessageCreate().WithContent("Music session is no longer active.").WithEphemeral(true))
		return
	}

	switch action {
	case "playpause":
		s.TogglePause()
		s.RefreshStatus()
	case "skip":
		_, _ = s.Skip()
	case "stop":
		GetVoiceManager().Leave(context.Background(), *guildID)
	case "loop":
		s.lockQueue()
		s.Looping = !s.Looping
		s.unlockQueue()
	case "autoplay":
		s.lockQueue()
		s.Autoplay = !s.Autoplay
		s.unlockQueue()
	case "volup":
		v := s.Volume.Load()
		if v < 200 {
			s.Volume.Store(v + 10)
		}
	case "voldown":
		v := s.Volume.Load()
		if v > 0 {
			s.Volume.Store(v - 10)
		}
	}

	UpdateVoicePanels(*guildID, *event.Client())
	_ = event.DeferUpdateMessage()
}

func handleMusicAutocomplete(event *events.AutocompleteInteractionCreate) {
	f := event.Data.Focused()
	if f.Name == "queue" {
		v := f.String()
		cs := []discord.AutocompleteChoice{
			discord.AutocompleteChoiceString{Name: "Play Now", Value: "now"},
			discord.AutocompleteChoiceString{Name: "Play Next", Value: "next"},
		}
		if v != "" {
			if _, err := strconv.Atoi(v); err == nil {
				cs = append([]discord.AutocompleteChoice{discord.AutocompleteChoiceString{Name: "Position: " + v, Value: v}}, cs...)
			}
		}
		_ = event.AutocompleteResult(cs)
		return
	}
	if f.Name != "query" {
		return
	}
	q := f.String()
	if q == "" {
		q = getRandomRecommendation(event.GuildID())
	} else if strings.Contains(q, "http") {
		_ = event.AutocompleteResult(nil)
		return
	}
	var rs []SearchResult
	var err error
	if strings.HasPrefix(strings.ToUpper(q), "[PL]") {
		if qBody := strings.TrimSpace(q[4:]); qBody != "" {
			if ytdlpRS, searchErr := GetVoiceManager().SearchPlaylist(qBody); searchErr == nil {
				for _, entry := range ytdlpRS {
					rs = append(rs, SearchResult{
						Title:       entry.Title,
						ChannelName: entry.Uploader,
						URL:         entry.URL,
					})
				}
			} else {
				err = searchErr
			}
		}
	} else {
		rs, err = GetVoiceManager().Search(q)
	}

	if err != nil {
		_ = event.AutocompleteResult(nil)
		return
	}

	var cs []discord.AutocompleteChoice
	for i, r := range rs {
		if i >= 25 {
			break
		}
		n, v := r.Title, r.URL
		if len(n) > 100 {
			n = n[:97] + "..."
		}
		if len(v) > 100 {
			v = r.Title
			if len(v) > 100 {
				v = v[:100]
			}
		}
		cs = append(cs, discord.AutocompleteChoiceString{Name: n, Value: v})
	}
	_ = event.AutocompleteResult(cs)
}

func startPlayback(ev *events.ApplicationCommandInteractionCreate, q, m string, a, l bool, p int) error {
	voiceSys.LogInfo("User %s (%s) requested playback: %s", ev.User().Username, ev.User().ID, q)
	vs, ok := mustGetUserVoiceState(ev)
	if !ok {
		return errors.New("user not in a voice channel")
	}
	vm := GetVoiceManager()
	s := vm.Prepare(*ev.Client(), *ev.GuildID(), *vs.ChannelID)
	s.lockQueue()
	s.Autoplay, s.Looping = a, l
	s.unlockQueue()
	je := make(chan error, 1)
	safeGo(func() { je <- vm.Join(context.Background(), *ev.Client(), *ev.GuildID(), *vs.ChannelID) })
	t, count, err := vm.Play(context.Background(), *ev.GuildID(), q, m, p)
	if err != nil {
		return err
	}
	if err := <-je; err != nil {
		return err
	}

	err = finishPlaybackResponse(ev, t, m, s.Autoplay, s.Looping, p, count)
	UpdateVoicePanels(*ev.GuildID(), *ev.Client())
	return err
}

func finishPlaybackResponse(ev *events.ApplicationCommandInteractionCreate, t *Track, m string, a, l bool, p int, count int) error {
	build := func() (string, string) {
		pr := "Added to queue:"
		if count > 1 {
			pr = fmt.Sprintf("📂 Added **%d** tracks from playlist to queue:", count)
			switch m {
			case "now":
				pr = fmt.Sprintf("▶️ Playing Now (Cleared queue and added **%d** tracks from playlist):", count)
			case "next":
				pr = fmt.Sprintf("⏭️ Added **%d** tracks to play next:", count)
			}
		} else {
			if m == "next" {
				pr = "⏭️ Next up:"
			} else if m == "now" {
				pr = "▶️ Playing Now (Skipped Current):"
			} else if p > 0 {
				pr = "Added to queue at position " + strconv.Itoa(p) + ":"
			}
		}

		var ss []string
		if a {
			ss = append(ss, "Autoplay")
		}
		if l {
			ss = append(ss, "Looping")
		}
		suffix := ""
		if len(ss) > 0 {
			suffix = " (" + strings.Join(ss, ", ") + ": Enabled)"
		}

		content := fmt.Sprintf("%s %s%s", pr, t.FullDisplay(), suffix)
		t.mu.Lock()
		art := t.ArtworkURL
		t.mu.Unlock()
		return content, art
	}

	content, art := build()
	var err error
	if art != "" {
		err = voiceSys.EditInteractionContainerV2(*ev.Client(), ev, voiceSys.NewV2Container(voiceSys.NewTextDisplay(content), voiceSys.NewMediaGallery(art)))
	} else {
		err = voiceSys.EditInteractionV2(*ev.Client(), ev, content)
	}

	select {
	case <-t.MetadataReady:
	default:
		safeGo(func() {
			select {
			case <-t.MetadataReady:
				c, a := build()
				if a != "" {
					_ = voiceSys.EditInteractionContainerV2(*ev.Client(), ev, voiceSys.NewV2Container(voiceSys.NewTextDisplay(c), voiceSys.NewMediaGallery(a)))
				} else {
					_ = voiceSys.EditInteractionV2(*ev.Client(), ev, c)
				}
			case <-time.After(15 * time.Second):
			}
		})
	}
	return err
}
