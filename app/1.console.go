package app

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

func handleBotConsole(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	ephemeral := true
	if eph, ok := data.OptBool("ephemeral"); ok {
		ephemeral = eph
	}
	if trunc, ok := data.OptBool("truncate"); ok && trunc {
		logPath := hooks.LogFile
		if logPath != "" {
			_ = os.Truncate(logPath, 0)
			hooks.LogInfo(MsgBotLogTruncated, event.User().Username)
		}
	}
	renderConsole(event, 20, 0, ephemeral)
}

func handleConsolePagination(event *events.ComponentInteractionCreate) {
	var (
		direction     string
		count, offset int
	)
	data := event.Data
	if menu, ok := data.(discord.StringSelectMenuInteractionData); ok {
		parts := strings.Split(menu.Values[0], ":")
		direction, count, offset = parts[0], Atoi(parts[1]), Atoi(parts[2])
	}
	newOffset := offset
	switch direction {
	case "up":
		newOffset += count
	case "down":
		newOffset -= count
		if newOffset < 0 {
			newOffset = 0
		}
	case "top":
		newOffset = 1000000
	case "bottom":
		newOffset = 0
	}
	renderConsole(event, count, newOffset, true)
}

func renderConsole(event any, count, offset int, ephemeral bool) {
	path := hooks.LogFile
	if path == "" {
		if ev, ok := event.(*events.ApplicationCommandInteractionCreate); ok {
			_ = hooks.RespondInteractionV2(*ev.Client(), ev, MsgBotConsoleDisabled, ephemeral)
		} else if ev, ok := event.(*events.ComponentInteractionCreate); ok {
			_ = hooks.EditInteractionV2(*ev.Client(), ev, MsgBotConsoleDisabled)
		}
		return
	}
	logs, hasMore, actual, err := readLogLines(path, count, offset)
	if err != nil {
		return
	}
	var (
		opts []discord.StringSelectMenuOption
	)
	if hasMore {
		opts = append(opts, discord.NewStringSelectMenuOption(MsgBotConsoleBtnOldest, fmt.Sprintf("top:%d:%d", count, actual)).WithDescription("Jump to oldest"))
		opts = append(opts, discord.NewStringSelectMenuOption(MsgBotConsoleBtnOlder, fmt.Sprintf("up:%d:%d", count, actual)).WithDescription("View older"))
	}
	opts = append(opts, discord.NewStringSelectMenuOption(MsgBotConsoleBtnRefresh, fmt.Sprintf("refresh:%d:%d", count, actual)).WithDescription("Reload current"))
	if actual > 0 {
		opts = append(opts, discord.NewStringSelectMenuOption(MsgBotConsoleBtnNewer, fmt.Sprintf("down:%d:%d", count, actual)).WithDescription("View newer"))
		opts = append(opts, discord.NewStringSelectMenuOption(MsgBotConsoleBtnLatest, fmt.Sprintf("bottom:%d:%d", count, actual)).WithDescription("Jump to latest"))
	}
	nav := discord.NewStringSelectMenu("console:nav", MsgConsoleNavLabel, opts...)
	container := discord.NewContainer(discord.NewTextDisplay(fmt.Sprintf("```ansi\n%s\n```", logs)), discord.NewActionRow(nav))
	if ev, ok := event.(*events.ComponentInteractionCreate); ok {
		_ = ev.UpdateMessage(discord.NewMessageUpdate().WithIsComponentsV2(true).WithComponents(container))
	} else if ev, ok := event.(*events.ApplicationCommandInteractionCreate); ok {
		_ = ev.CreateMessage(discord.NewMessageCreate().WithIsComponentsV2(true).WithEphemeral(ephemeral).WithComponents(container))
	}
}

func readLogLines(path string, count, offset int) (string, bool, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, 0, err
	}
	defer f.Close()
	s, _ := f.Stat()
	if s.Size() == 0 {
		return "", false, 0, nil
	}
	buf := make([]byte, 8192)
	cur := s.Size()
	var (
		offs []int64
	)
	offs = append(offs, s.Size())
	limit := offset + count + 1
	for cur > 0 && len(offs) <= limit {
		sz := int64(8192)
		if cur < sz {
			sz = cur
		}
		cur -= sz
		_, _ = f.ReadAt(buf[:sz], cur)
		chunk := buf[:sz]
		for {
			idx := bytes.LastIndexByte(chunk, '\n')
			if idx == -1 {
				break
			}
			pos := cur + int64(idx)
			if pos != s.Size()-1 {
				offs = append(offs, pos)
				if len(offs) > limit {
					break
				}
			}
			chunk = chunk[:idx]
		}
	}
	if cur == 0 && (len(offs) == 1 || offs[len(offs)-1] != 0) {
		offs = append(offs, 0)
	}
	found := len(offs) - 1
	actual := max(min(offset, found-count), 0)
	e, st := offs[actual], offs[Min(actual+count, found)]
	if st > 0 {
		st++
	}
	length := e - st
	const maxR = 2 * 1024 * 1024
	if length > maxR {
		st = e - maxR
		length = maxR
	}
	if length <= 0 {
		return MsgBotConsoleEmpty, actual+count < found, actual, nil
	}
	res := make([]byte, length)
	_, _ = f.ReadAt(res, st)
	logs := strings.TrimSpace(string(res))
	if len(logs) > 1950 {
		cut := len(logs) - 1950
		if nl := strings.IndexByte(logs[cut:], '\n'); nl != -1 {
			logs = logs[cut+nl+1:]
		} else {
			logs = logs[cut:]
		}
	}
	return logs, actual+count < found, actual, nil
}
