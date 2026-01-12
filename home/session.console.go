package home

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleSessionConsole(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	ephemeral := true
	if eph, ok := data.OptBool("ephemeral"); ok {
		ephemeral = eph
	}

	if trunc, ok := data.OptBool("truncate"); ok && trunc {
		logPath := sys.GetLogPath()
		if logPath != "" {
			_ = os.Truncate(logPath, 0)
			sys.LogInfo("Log file truncated by user %s", event.User().Username)
		}
	}

	renderConsole(event, 20, 0, ephemeral)
}

func handleConsolePagination(event *events.ComponentInteractionCreate) {
	customID := event.Data.CustomID()
	parts := strings.Split(customID, ":")
	if len(parts) < 4 {
		return
	}

	// Format: console:direction:count:offset
	direction := parts[1]
	count, _ := strconv.Atoi(parts[2])
	offset, _ := strconv.Atoi(parts[3])

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
		newOffset = 1000000 // Sentinel for oldest
	case "bottom":
		newOffset = 0
	case "refresh":
	}

	renderConsole(event, count, newOffset, true) // Ephemeral for pagination is irrelevant as it updates existing message
}

func renderConsole(event any, count int, offset int, ephemeral bool) {
	logPath := sys.GetLogPath()
	if logPath == "" {
		msg := sys.MsgSessionConsoleDisabled
		if ev, ok := event.(*events.ApplicationCommandInteractionCreate); ok {
			_ = ev.CreateMessage(discord.NewMessageCreateBuilder().SetContent(msg).SetEphemeral(ephemeral).Build())
		} else if ev, ok := event.(*events.ComponentInteractionCreate); ok {
			_ = ev.UpdateMessage(discord.NewMessageUpdateBuilder().AddComponents(discord.NewContainer(discord.NewTextDisplay(msg))).Build())
		}
		return
	}

	logs, hasMoreOld, actualOffset, err := readLogLines(logPath, count, offset)
	if err != nil {
		sys.LogError(sys.MsgSessionLogReadFail, err)
		return
	}

	content := fmt.Sprintf("```ansi\n%s\n```", logs)

	var components []discord.InteractiveComponent

	topBtn := discord.NewSecondaryButton(sys.MsgSessionConsoleBtnOldest, fmt.Sprintf("console:top:%d:%d", count, actualOffset))
	if !hasMoreOld {
		topBtn = topBtn.AsDisabled()
	}
	components = append(components, topBtn)

	upBtn := discord.NewSecondaryButton(sys.MsgSessionConsoleBtnOlder, fmt.Sprintf("console:up:%d:%d", count, actualOffset))
	if !hasMoreOld {
		upBtn = upBtn.AsDisabled()
	}
	components = append(components, upBtn)

	refreshBtn := discord.NewSecondaryButton(sys.MsgSessionConsoleBtnRefresh, fmt.Sprintf("console:refresh:%d:%d", count, actualOffset))
	components = append(components, refreshBtn)

	downBtn := discord.NewSecondaryButton(sys.MsgSessionConsoleBtnNewer, fmt.Sprintf("console:down:%d:%d", count, actualOffset))
	if actualOffset <= 0 {
		downBtn = downBtn.AsDisabled()
	}
	components = append(components, downBtn)

	bottomBtn := discord.NewSecondaryButton(sys.MsgSessionConsoleBtnLatest, fmt.Sprintf("console:bottom:%d:%d", count, actualOffset))
	if actualOffset <= 0 {
		bottomBtn = bottomBtn.AsDisabled()
	}
	components = append(components, bottomBtn)

	display := discord.NewTextDisplay(content)
	container := discord.NewContainer(display, discord.NewActionRow(components...))

	if ev, ok := event.(*events.ComponentInteractionCreate); ok {
		_ = ev.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			SetComponents(container).
			Build())
	} else if ev, ok := event.(*events.ApplicationCommandInteractionCreate); ok {
		_ = ev.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			SetEphemeral(ephemeral).
			AddComponents(container).
			Build())
	}
}

// readLogLines reads specific slice of the file efficiently.
func readLogLines(filepath string, count int, offset int) (string, bool, int, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return "", false, 0, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return "", false, 0, err
	}

	filesize := stat.Size()
	if filesize == 0 {
		return "", false, 0, nil
	}

	const chunkSize = 8192
	buf := make([]byte, chunkSize)
	cursor := filesize
	var lineOffsets []int64
	lineOffsets = append(lineOffsets, filesize) // EOF is the first "boundary"

	// Bounded backward scan.
	targetCount := offset + count + 1

	for cursor > 0 && len(lineOffsets) <= targetCount {
		readSize := int64(chunkSize)
		if cursor < readSize {
			readSize = cursor
		}
		cursor -= readSize

		_, err := file.ReadAt(buf[:readSize], cursor)
		if err != nil {
			return "", false, 0, err
		}

		chunk := buf[:readSize]
		for {
			idx := bytes.LastIndexByte(chunk, '\n')
			if idx == -1 {
				break
			}
			pos := cursor + int64(idx)
			// Skip trailing newline if it's the absolute end of file
			if pos != filesize-1 {
				lineOffsets = append(lineOffsets, pos)
				if len(lineOffsets) > targetCount {
					break
				}
			}
			chunk = chunk[:idx]
		}
	}

	// Beginning of file is the ultimate boundary
	if cursor == 0 && (len(lineOffsets) == 1 || lineOffsets[len(lineOffsets)-1] != 0) {
		lineOffsets = append(lineOffsets, 0)
	}

	totalFound := len(lineOffsets) - 1
	actualOffset := offset
	if actualOffset > totalFound-count {
		actualOffset = totalFound - count
	}
	if actualOffset < 0 {
		actualOffset = 0
	}

	endIdx := actualOffset
	startIdx := actualOffset + count
	if startIdx > totalFound {
		startIdx = totalFound
	}

	endPos := lineOffsets[endIdx]
	startPos := lineOffsets[startIdx]

	if startPos > 0 {
		startPos++ // Move past the newline
	}

	hasMoreOld := startIdx < totalFound

	// Read the actual window
	length := endPos - startPos

	// Safety: Cap internal read at 2MB to avoid OOM on malformed or extreme log lines.
	const maxRead = 2 * 1024 * 1024
	if length > maxRead {
		startPos = endPos - maxRead
		length = maxRead
	}

	if length <= 0 {
		return sys.MsgSessionConsoleEmpty, hasMoreOld, actualOffset, nil
	}

	result := make([]byte, length)
	_, err = file.ReadAt(result, startPos)
	if err != nil {
		return "", false, 0, err
	}

	logs := string(result)

	// Truncate to Discord's limit while preserving ANSI codes.
	if len(logs) > 1950 {
		cutPoint := len(logs) - 1950
		// Look for the first newline after the cut point to keep it clean
		if nextNL := strings.IndexByte(logs[cutPoint:], '\n'); nextNL != -1 {
			logs = logs[cutPoint+nextNL+1:]
		} else {
			logs = logs[cutPoint:]
		}

		escIdx := strings.Index(logs, "\x1b[")
		mIdx := strings.IndexByte(logs, 'm')
		if mIdx != -1 && (escIdx == -1 || mIdx < escIdx) {
			logs = logs[mIdx+1:]
		}
	}

	return strings.TrimSpace(logs), hasMoreOld, actualOffset, nil
}

func init() {
	sys.RegisterComponentHandler("console:", handleConsolePagination)
}
