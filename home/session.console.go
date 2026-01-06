package home

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func init() {
	sys.RegisterComponentHandler("console:", handleConsolePagination)
}

func handleSessionConsole(event *events.ApplicationCommandInteractionCreate) {
	renderConsole(event, 20, 0)
}

func handleConsolePagination(event *events.ComponentInteractionCreate) {
	customID := event.Data.CustomID()
	parts := strings.Split(customID, ":")
	if len(parts) < 4 {
		return
	}

	// Format: console:direction:count:offset
	direction := parts[1]
	count := 20
	offset := 0
	fmt.Sscanf(parts[2], "%d", &count)
	fmt.Sscanf(parts[3], "%d", &offset)

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
		// Keep offset as is
	}

	renderConsole(event, count, newOffset)
}

func renderConsole(event any, count int, offset int) {
	logPath := sys.GetLogPath()
	if logPath == "" {
		msg := "Logging to file is disabled."
		if ev, ok := event.(*events.ApplicationCommandInteractionCreate); ok {
			_ = ev.CreateMessage(discord.NewMessageCreateBuilder().SetContent(msg).SetEphemeral(true).Build())
		}
		return
	}

	logs, hasMoreOld, actualOffset, err := readLogLines(logPath, count, offset)
	if err != nil {
		sys.LogError("Failed to read log file: %v", err)
		return
	}

	content := fmt.Sprintf("```ansi\n%s\n```", logs)

	// Action buttons
	var components []discord.InteractiveComponent

	// Page Up (Jump to oldest)
	topBtn := discord.NewSecondaryButton("⏫", fmt.Sprintf("console:top:%d:%d", count, actualOffset))
	if !hasMoreOld {
		topBtn = topBtn.AsDisabled()
	}
	components = append(components, topBtn)

	// Up (Older)
	upBtn := discord.NewSecondaryButton("⬆️", fmt.Sprintf("console:up:%d:%d", count, actualOffset))
	if !hasMoreOld {
		upBtn = upBtn.AsDisabled()
	}
	components = append(components, upBtn)

	// Refresh
	refreshBtn := discord.NewSecondaryButton("🔄", fmt.Sprintf("console:refresh:%d:%d", count, actualOffset))
	components = append(components, refreshBtn)

	// Down (Newer)
	downBtn := discord.NewSecondaryButton("⬇️", fmt.Sprintf("console:down:%d:%d", count, actualOffset))
	if actualOffset <= 0 {
		downBtn = downBtn.AsDisabled()
	}
	components = append(components, downBtn)

	// Page Down (Jump to latest)
	bottomBtn := discord.NewSecondaryButton("⏬", fmt.Sprintf("console:bottom:%d:%d", count, actualOffset))
	if actualOffset <= 0 {
		bottomBtn = bottomBtn.AsDisabled()
	}
	components = append(components, bottomBtn)

	// Assemble component V2 container
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

	// Optimized Scan:
	// We want to skip 'offset' lines from the end to find endPos.
	// Then we skip 'count' lines further to find startPos.
	// We perform this in one backward pass using bytes.LastIndexByte.

	const chunkSize = 8192
	buf := make([]byte, chunkSize)
	cursor := filesize
	var lineOffsets []int64
	lineOffsets = append(lineOffsets, filesize) // EOF is the first "boundary"

	// Bounded Scan: We scan until we find enough lines or hit start of file.
	// If offset is very large (Top), this will naturally scan the whole file.
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
	if length <= 0 {
		return "No logs available.", hasMoreOld, actualOffset, nil
	}

	result := make([]byte, length)
	_, err = file.ReadAt(result, startPos)
	if err != nil {
		return "", false, 0, err
	}

	logs := string(result)

	// Optimization: Discord ANSI Truncation
	// If logs are > 1950 chars, we truncate while ensuring we don't break ANSI codes.
	// ANSI codes look like \x1b[...m
	if len(logs) > 1950 {
		cutPoint := len(logs) - 1950
		// Look for the first newline after the cut point to keep it clean
		if nextNL := strings.IndexByte(logs[cutPoint:], '\n'); nextNL != -1 {
			logs = logs[cutPoint+nextNL+1:]
		} else {
			logs = logs[cutPoint:]
		}

		// Ensure we didn't cut into an ANSI sequence
		// If the first '[' is preceded by '\x1b', we are fine.
		// If we find 'm' before seeing '\x1b[', we likely cut a sequence.
		escIdx := strings.Index(logs, "\x1b[")
		mIdx := strings.IndexByte(logs, 'm')
		if mIdx != -1 && (escIdx == -1 || mIdx < escIdx) {
			// We cut inside a sequence. Find the end of this sequence and strip it.
			logs = logs[mIdx+1:]
		}
	}

	return strings.TrimSpace(logs), hasMoreOld, actualOffset, nil
}
