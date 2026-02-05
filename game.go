package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/corentings/chess/v2"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

// ===========================
// Command Registration
// ===========================

func init() {
	RegisterCommand(discord.SlashCommandCreate{
		Name:        "game",
		Description: "Good games.",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "connect4",
				Description: "Play Connect Four against another player or AI",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionUser{
						Name:        "opponent",
						Description: "Challenge another user (leave empty to play against AI)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "difficulty",
						Description: "AI difficulty level (only for AI games)",
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Easy", Value: "easy"},
							{Name: "Normal", Value: "normal"},
							{Name: "Hard", Value: "hard"},
						},
					},
					discord.ApplicationCommandOptionInt{
						Name:        "timer",
						Description: "Turn timer in seconds (leave empty or 0 to disable)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "size",
						Description: "Board size",
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Small (5x4)", Value: "small"},
							{Name: "Classic (7x6)", Value: "classic"},
							{Name: "Large (9x8)", Value: "large"},
							{Name: "Master (10x10)", Value: "master"},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "checkers",
				Description: "Play Checkers (Draughts) against another player or AI",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionUser{
						Name:        "opponent",
						Description: "Challenge another user (leave empty to play against AI)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "difficulty",
						Description: "AI difficulty level (only for AI games)",
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Easy", Value: "easy"},
							{Name: "Normal", Value: "normal"},
							{Name: "Hard", Value: "hard"},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "chess",
				Description: "Play Chess against another player or AI",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionUser{
						Name:        "opponent",
						Description: "Challenge another user (leave empty to play against AI)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "difficulty",
						Description: "AI difficulty level (only for AI games)",
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Easy", Value: "easy"},
							{Name: "Normal", Value: "normal"},
							{Name: "Hard", Value: "hard"},
						},
					},
				},
			},
		},
	}, func(event *events.ApplicationCommandInteractionCreate) {
		data := event.SlashCommandInteractionData()
		subCmd := data.SubCommandName
		if subCmd == nil {
			return
		}

		switch *subCmd {
		case "connect4":
			handlePlayConnect4(event, data)
		case "checkers":
			HandlePlayCheckers(event, data)
		case "chess":
			HandlePlayChess(event, data)
		}
	})

	RegisterComponentHandler("connect4:", connect4HandleMove)
	RegisterComponentHandler("checkers:", HandleCheckersInteraction)
	RegisterComponentHandler("chess:", HandleChessInteraction)
}

// ===========================
// Global State & Initialization
// ===========================

var (
	activeConnect4Games   = make(map[string]*connect4Game)
	activeConnect4GamesMu sync.RWMutex

	activeCheckersGames   = make(map[string]*CheckersGame)
	activeCheckersGamesMu sync.RWMutex

	activeChessGames   = make(map[string]*ChessGame)
	activeChessGamesMu sync.RWMutex

	userActiveGame   = make(map[snowflake.ID]string)
	userActiveGameMu sync.RWMutex
)

type GameColorVariant int

const (
	VariantStandard GameColorVariant = iota
	VariantInverted
)

// ===========================
// Connect Four Game Constants & Types
// ===========================

const (
	connect4ToWin   = 4
	connect4P1      = "🔵"
	connect4P2      = "🔴"
	connect4Empty   = "⚫"
	connect4Forfeit = "Forfeit"
	connect4P1Win   = "🟦"
	connect4P2Win   = "🟥"

	connect4StatusTurn     = "**<@%d>'s Turn** %s"
	connect4StatusDraw     = "**<@%d> and <@%d> ended with a Draw!**"
	connect4StatusWin      = "**<@%d> Lost 💩 - <@%d> Won! 🎉**"
	connect4StatusForfeit  = "**<@%d> Forfeited 🛑 - <@%d> Won! 🎉**"
	connect4StatusTimeout  = "**<@%d> Took Too Long ⏱️ - <@%d> Won! 🎉**"
	connect4StatusInactive = "**❌ `This game is no longer active.`**"
	connect4StatusFull     = "**❌ `Column is full.`**"
)

var (
	connect4ColumnEmojis = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟"}
)

// connect4Difficulty represents the AI difficulty level for Connect Four
type connect4Difficulty int

const (
	connect4Easy connect4Difficulty = iota
	connect4Normal
	connect4Hard
)

// connect4Game represents a Connect Four game session with all its state
type connect4Game struct {
	board         [][]int            // Game board (0=empty, 1=player1, 2=player2)
	rows          int                // Number of rows
	cols          int                // Number of columns
	player1ID     snowflake.ID       // Player 1's Discord ID
	player2ID     snowflake.ID       // Player 2's Discord ID
	isAI          bool               // Whether this is a PvAI game
	aiDifficulty  connect4Difficulty // AI difficulty level
	aiPlayerNum   int                // Which player is AI (1 or 2)
	colorVariant  GameColorVariant   // Color variant (standard or inverted)
	currentTurn   int                // Current turn (1 or 2)
	gameOver      bool               // Whether the game has ended
	winner        int                // Winner (0=draw, 1=player1, 2=player2)
	winCells      [][2]int           // Coordinates of winning cells
	moveCount     int                // Total moves made
	timerEnabled  bool               // Whether turn timer is enabled
	timerDuration time.Duration      // Duration for each turn
	turnTimer     *time.Timer        // Timer for the current turn
	lastMoveTime  time.Time          // Time of last move
	messageID     snowflake.ID       // Discord message ID
	channelID     snowflake.ID       // Discord channel ID
	originalP1ID  snowflake.ID       // Original player 1 ID (for replays)
	originalP2ID  snowflake.ID       // Original player 2 ID (for replays)
}

// ===========================
// Interaction Handlers
// ===========================

// handlePlayConnect4 initiates a new game session (Slash Command)
func handlePlayConnect4(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	defer func() {
		if r := recover(); r != nil {
			LogError("Panic in handlePlayConnect4: %v", r)
			fmt.Printf("%s\n", debug.Stack())
		}
	}()

	var opponentID *snowflake.ID
	difficulty := connect4Normal

	var isOpponentBot bool
	if opponent, ok := data.OptUser("opponent"); ok {
		opponentID = &opponent.ID
		isOpponentBot = opponent.Bot
	}

	if diff, ok := data.OptString("difficulty"); ok {
		switch diff {
		case "easy":
			difficulty = connect4Easy
		case "normal":
			difficulty = connect4Normal
		case "hard":
			difficulty = connect4Hard
		}
	}

	timerSeconds := 0
	if timer, ok := data.OptInt("timer"); ok {
		timerSeconds = timer
	}

	rows, cols := 6, 7
	if size, ok := data.OptString("size"); ok {
		switch size {
		case "small":
			rows, cols = 4, 5
		case "classic":
			rows, cols = 6, 7
		case "large":
			rows, cols = 8, 9
		case "master":
			rows, cols = 10, 10
		}
	}

	cid := event.Channel().ID()
	gameID := fmt.Sprintf("connect4_%d_%d", cid, time.Now().UnixNano())

	appID := event.ApplicationID()
	p1 := event.User().ID

	userActiveGameMu.Lock()
	if gid, ok := userActiveGame[p1]; ok {
		userActiveGameMu.Unlock()
		event.CreateMessage(discord.NewMessageCreateBuilder().SetContent(fmt.Sprintf("You are already in a game! (ID: %s)", gid)).SetEphemeral(true).Build())
		return
	}
	if opponentID != nil && *opponentID != appID {
		if gid, ok := userActiveGame[*opponentID]; ok {
			userActiveGameMu.Unlock()
			event.CreateMessage(discord.NewMessageCreateBuilder().SetContent(fmt.Sprintf("<@%d> is already in a game! (ID: %s)", *opponentID, gid)).SetEphemeral(true).Build())
			return
		}
	}
	userActiveGame[p1] = gameID
	if opponentID != nil && *opponentID != appID {
		userActiveGame[*opponentID] = gameID
	}
	userActiveGameMu.Unlock()

	game := &connect4Game{
		rows:          rows,
		cols:          cols,
		player1ID:     p1,
		currentTurn:   1,
		timerEnabled:  timerSeconds > 0,
		timerDuration: time.Duration(timerSeconds) * time.Second,
		lastMoveTime:  time.Now(),
		channelID:     cid,
	}
	game.board = make([][]int, rows)
	for i := range game.board {
		game.board[i] = make([]int, cols)
	}

	var p2 snowflake.ID
	if opponentID == nil || *opponentID == appID || isOpponentBot {
		if opponentID != nil {
			p2 = *opponentID
		} else {
			p2 = appID
		}
		game.isAI = true
		game.aiDifficulty = difficulty
	} else {
		p2 = *opponentID
		game.isAI = false
	}

	game.originalP1ID = p1
	game.originalP2ID = p2
	var player1, player2 snowflake.ID
	if rand.Intn(2) == 0 {
		player1 = p1
		player2 = p2
	} else {
		player1 = p2
		player2 = p1
	}

	aiPlayerNum := 0
	if game.isAI {
		if player1 == appID || (opponentID != nil && player1 == *opponentID && game.isAI) {
			aiPlayerNum = 1
		} else {
			aiPlayerNum = 2
		}
	}

	game.player1ID = player1
	game.player2ID = player2
	game.aiPlayerNum = aiPlayerNum

	variant := VariantStandard
	if rand.Intn(2) == 1 {
		variant = VariantInverted
	}
	game.colorVariant = variant

	activeConnect4GamesMu.Lock()
	activeConnect4Games[gameID] = game
	activeConnect4GamesMu.Unlock()

	builder := connect4BuildMessage(game, gameID, "")
	if err := event.CreateMessage(builder); err != nil {
		activeConnect4GamesMu.Lock()
		delete(activeConnect4Games, gameID)
		activeConnect4GamesMu.Unlock()

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
		return
	}

	msg, err := event.Client().Rest.GetInteractionResponse(event.ApplicationID(), event.Token())
	if err == nil && msg != nil {
		game.messageID = msg.ID
	}

	if game.timerEnabled {
		connect4StartTimer(event.Client(), game, gameID, 0)
	}

	if game.isAI && game.aiPlayerNum == 1 {
		time.AfterFunc(1*time.Second, func() {
			connect4MakeAIMove(event.Client(), game, gameID)
		})
	}
}

// connect4StartTimer watches for turn expiry and forfeits if time runs out
func connect4StartTimer(client *bot.Client, game *connect4Game, gameID string, moveCount int) {
	activeConnect4GamesMu.Lock()
	if game.turnTimer != nil {
		game.turnTimer.Stop()
	}
	game.turnTimer = time.AfterFunc(game.timerDuration, func() {
		activeConnect4GamesMu.Lock()
		defer activeConnect4GamesMu.Unlock()

		if game.moveCount != moveCount || game.gameOver || game.messageID == 0 {
			return
		}

		if _, exists := activeConnect4Games[gameID]; !exists {
			return
		}

		winnerID := game.player1ID
		loserID := game.player2ID
		if game.currentTurn == 1 {
			winnerID = game.player2ID
			loserID = game.player1ID
		}

		game.winner = 2
		if game.currentTurn == 2 {
			game.winner = 1
		}

		game.gameOver = true

		status := fmt.Sprintf(connect4StatusTimeout, loserID, winnerID)
		builder := connect4BuildMessage(game, gameID, status)
		if client != nil {
			_, _ = client.Rest.UpdateMessage(game.channelID, game.messageID, discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				SetComponents(builder.Components...).
				Build())
		}

		delete(activeConnect4Games, gameID)
		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
	})
	activeConnect4GamesMu.Unlock()
}

// connect4HandleMove processes player moves and forfeits (Component Interaction)
func connect4HandleMove(event *events.ComponentInteractionCreate) {
	parts := strings.Split(event.Data.CustomID(), ":")
	if len(parts) < 3 {
		event.DeferUpdateMessage()
		return
	}
	gameID := parts[1]
	action := parts[2]

	activeConnect4GamesMu.Lock()
	game, exists := activeConnect4Games[gameID]
	if !exists {
		activeConnect4GamesMu.Unlock()
		if event.Message.ID == 0 {
			event.DeferUpdateMessage()
			return
		}

		var newComponents []discord.LayoutComponent
		for i, comp := range event.Message.Components {
			modified := connect4DisableInteractive(comp)
			if i == 0 {
				if td, ok := modified.(discord.TextDisplayComponent); ok {
					modified = discord.NewTextDisplay(td.Content + "\n" + connect4StatusInactive)
				} else if td, ok := modified.(*discord.TextDisplayComponent); ok {
					modified = discord.NewTextDisplay(td.Content + "\n" + connect4StatusInactive)
				}
			}
			newComponents = append(newComponents, modified)
		}

		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			SetComponents(newComponents...).
			Build())
		return
	}

	if game.gameOver && action != "yes" && action != "no" {
		activeConnect4GamesMu.Unlock()
		event.DeferUpdateMessage()
		return
	}
	activeConnect4GamesMu.Unlock()

	if action == "yes" || action == "no" {
		userID := event.User().ID
		if userID != game.originalP1ID && userID != game.originalP2ID {
			event.CreateMessage(discord.NewMessageCreateBuilder().
				SetContent("You're not a player in this game!").
				SetEphemeral(true).
				Build())
			return
		}

		if action == "no" {
			activeConnect4GamesMu.Lock()
			if game.turnTimer != nil {
				game.turnTimer.Stop()
			}
			delete(activeConnect4Games, gameID)
			activeConnect4GamesMu.Unlock()

			userActiveGameMu.Lock()
			delete(userActiveGame, game.originalP1ID)
			delete(userActiveGame, game.originalP2ID)
			userActiveGameMu.Unlock()

			fullMsg := connect4BuildMessage(game, gameID, "")

			var newComponents []discord.LayoutComponent

			if len(fullMsg.Components) > 3 {
				newComponents = append(newComponents, fullMsg.Components[:3]...)
			} else {
				newComponents = append(newComponents, fullMsg.Components...)
			}

			_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				SetComponents(newComponents...).
				Build())
			return
		}

		activeConnect4GamesMu.Lock()
		for r := 0; r < game.rows; r++ {
			for c := 0; c < game.cols; c++ {
				game.board[r][c] = 0
			}
		}
		game.gameOver = false
		game.winner = 0
		game.winCells = nil
		game.moveCount = 0
		game.lastMoveTime = time.Now()
		game.currentTurn = 1

		if rand.Intn(2) == 0 {
			game.player1ID = game.originalP1ID
			game.player2ID = game.originalP2ID
			if game.isAI {
				game.aiPlayerNum = 2
			}
		} else {
			game.player1ID = game.originalP2ID
			game.player2ID = game.originalP1ID
			if game.isAI {
				game.aiPlayerNum = 1
			}
		}
		activeConnect4GamesMu.Unlock()

		builder := connect4BuildMessage(game, gameID, "🔄 Game Restarted!")
		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetComponents(builder.Components...).
			Build())

		if game.timerEnabled {
			connect4StartTimer(event.Client(), game, gameID, 0)
		}
		if game.isAI && game.aiPlayerNum == 1 {
			time.AfterFunc(1*time.Second, func() {
				connect4MakeAIMove(event.Client(), game, gameID)
			})
		}
		return
	}

	if action == "forfeit" {
		userID := event.User().ID
		if userID != game.player1ID && userID != game.player2ID {
			event.CreateMessage(discord.NewMessageCreateBuilder().
				SetContent("You're not a player in this game!").
				SetEphemeral(true).
				Build())
			return
		}

		activeConnect4GamesMu.Lock()
		if game.turnTimer != nil {
			game.turnTimer.Stop()
		}
		game.gameOver = true
		var forfeitMsg string
		if userID == game.player1ID {
			game.winner = 2
			forfeitMsg = fmt.Sprintf(connect4StatusForfeit, game.player1ID, game.player2ID)
		} else {
			game.winner = 1
			forfeitMsg = fmt.Sprintf(connect4StatusForfeit, game.player2ID, game.player1ID)
		}
		activeConnect4GamesMu.Unlock()

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()

		builder := connect4BuildMessage(game, gameID, forfeitMsg)
		event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetComponents(builder.Components...).
			Build())
		return
	}

	col, err := strconv.Atoi(action)
	if err != nil {
		event.DeferUpdateMessage()
		return
	}
	col--

	userID := event.User().ID
	expectedPlayerID := game.player1ID
	if game.currentTurn == 2 {
		expectedPlayerID = game.player2ID
	}

	if userID != expectedPlayerID {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetContent("It's not your turn!").
			SetEphemeral(true).
			Build())
		return
	}

	activeConnect4GamesMu.Lock()
	if game.gameOver && action != "yes" && action != "no" {
		activeConnect4GamesMu.Unlock()
		event.DeferUpdateMessage()
		return
	}

	if game.turnTimer != nil {
		game.turnTimer.Stop()
	}

	statusMsg := connect4MakeMove(game, col)
	activeConnect4GamesMu.Unlock()

	builder := connect4BuildMessage(game, gameID, statusMsg)
	event.UpdateMessage(discord.NewMessageUpdateBuilder().
		SetComponents(builder.Components...).
		Build())

	if !game.gameOver {
		if game.timerEnabled {
			connect4StartTimer(event.Client(), game, gameID, game.moveCount)
		}
		if game.isAI && game.currentTurn == game.aiPlayerNum {
			time.AfterFunc(1*time.Second, func() {
				connect4MakeAIMove(event.Client(), game, gameID)
			})
		}
	}
}

// ===========================
// UI & Rendering
// ===========================

// connect4BuildMessage constructs the Discord message for the current game state
func connect4BuildMessage(game *connect4Game, gameID string, statusMsg string) discord.MessageCreate {
	var sb strings.Builder
	header := connect4GetHeader(game.cols)
	sb.WriteString(header + "\n")

	// Determine colors based on variant
	p1Emoji, p2Emoji := connect4P1, connect4P2
	p1WinEmoji, p2WinEmoji := connect4P1Win, connect4P2Win
	if game.colorVariant == VariantInverted {
		p1Emoji, p2Emoji = connect4P2, connect4P1
		p1WinEmoji, p2WinEmoji = connect4P2Win, connect4P1Win
	}

	for row := 0; row < game.rows; row++ {
		for col := 0; col < game.cols; col++ {
			isWinCell := false
			if game.gameOver && game.winner != 0 {
				for _, cell := range game.winCells {
					if cell[0] == row && cell[1] == col {
						isWinCell = true
						break
					}
				}
			}

			if isWinCell {
				if game.winner == 1 {
					sb.WriteString(p1WinEmoji)
				} else {
					sb.WriteString(p2WinEmoji)
				}
			} else {
				switch game.board[row][col] {
				case 0:
					sb.WriteString(connect4Empty)
				case 1:
					sb.WriteString(p1Emoji)
				case 2:
					sb.WriteString(p2Emoji)
				}
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString(header + "\n")

	var statusSB strings.Builder
	if game.gameOver {
		if statusMsg != "" {
			statusSB.WriteString(statusMsg)
		} else if game.winner == 0 {
			statusSB.WriteString(fmt.Sprintf(connect4StatusDraw, game.player1ID, game.player2ID))
		} else {
			winnerID := game.player1ID
			loserID := game.player2ID
			if game.winner == 2 {
				winnerID = game.player2ID
				loserID = game.player1ID
			}
			statusSB.WriteString(fmt.Sprintf(connect4StatusWin, loserID, winnerID))
		}
	} else {
		currentPlayerID := game.player1ID
		currentSymbol := p1Emoji
		if game.currentTurn == 2 {
			currentPlayerID = game.player2ID
			currentSymbol = p2Emoji
		}

		statusSB.WriteString(fmt.Sprintf(connect4StatusTurn, currentPlayerID, currentSymbol))

		if statusMsg != "" {
			statusSB.WriteString("\n" + statusMsg)
		}

		if game.timerEnabled {
			expires := game.lastMoveTime.Add(game.timerDuration)
			statusSB.WriteString(fmt.Sprintf("\n⏱️ Expires <t:%d:R>", expires.Unix()))
		}
	}

	var layoutComponents []discord.LayoutComponent

	layoutComponents = append(layoutComponents, discord.NewTextDisplay(statusSB.String()))
	layoutComponents = append(layoutComponents, discord.NewTextDisplay(sb.String()))

	if game.gameOver {
		layoutComponents = append(layoutComponents, discord.NewSeparator(discord.SeparatorSpacingSizeSmall).WithDivider(true))

		row := discord.NewActionRow(
			discord.NewButton(discord.ButtonStyleSecondary, "Play Again?", "connect4:disabled:info", "", 0).WithDisabled(true),
			discord.NewButton(discord.ButtonStyleSuccess, "Yes!", fmt.Sprintf("connect4:%s:yes", gameID), "", 0),
			discord.NewButton(discord.ButtonStyleDanger, "No.", fmt.Sprintf("connect4:%s:no", gameID), "", 0),
		)
		layoutComponents = append(layoutComponents, row)
	} else {
		layoutComponents = append(layoutComponents, discord.NewSeparator(discord.SeparatorSpacingSizeSmall).WithDivider(true))

		for i := 0; i < game.cols; i += 5 {
			var rowButtons []discord.InteractiveComponent
			end := min(i+5, game.cols)
			for col := i + 1; col <= end; col++ {
				customID := fmt.Sprintf("connect4:%s:%d", gameID, col)
				btn := discord.NewButton(discord.ButtonStylePrimary, connect4ColumnEmojis[col-1], customID, "", 0)
				if connect4IsColumnFull(game, col-1) {
					btn = btn.WithDisabled(true)
				}
				rowButtons = append(rowButtons, btn)
			}
			layoutComponents = append(layoutComponents, discord.NewActionRow(rowButtons...))
		}

		forfeitBtn := discord.NewButton(discord.ButtonStyleDanger, connect4Forfeit, fmt.Sprintf("connect4:%s:forfeit", gameID), "", 0)
		layoutComponents = append(layoutComponents, discord.NewActionRow(forfeitBtn))
	}

	return discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(layoutComponents...).
		Build()
}

// ===========================
// Game Logic
// ===========================

// connect4MakeMove attempts to place a piece in the specified column
func connect4MakeMove(game *connect4Game, col int) string {
	if game.gameOver {
		return "Game is already over."
	}
	row := -1
	for r := game.rows - 1; r >= 0; r-- {
		if game.board[r][col] == 0 {
			row = r
			break
		}
	}

	if row == -1 {
		return connect4StatusFull
	}

	game.board[row][col] = game.currentTurn
	game.lastMoveTime = time.Now()
	game.moveCount++

	if won, cells := connect4CheckWin(game, row, col); won {
		game.gameOver = true
		game.winner = game.currentTurn
		game.winCells = cells

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
	} else if connect4IsBoardFull(game) {
		game.gameOver = true
		game.winner = 0
		game.currentTurn = 1

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
	} else {
		if game.currentTurn == 1 {
			game.currentTurn = 2
		} else {
			game.currentTurn = 1
		}
	}

	return ""
}

// connect4CheckWin checks if the last move at (row, col) resulted in a win
func connect4CheckWin(game *connect4Game, row, col int) (bool, [][2]int) {
	player := game.board[row][col]
	if player == 0 {
		return false, nil
	}

	collect := func(dr, dc int) [][2]int {
		cells := [][2]int{{row, col}}
		for r, c := row+dr, col+dc; r >= 0 && r < game.rows && c >= 0 && c < game.cols && game.board[r][c] == player; r, c = r+dr, c+dc {
			cells = append(cells, [2]int{r, c})
		}
		for r, c := row-dr, col-dc; r >= 0 && r < game.rows && c >= 0 && c < game.cols && game.board[r][c] == player; r, c = r-dr, c-dc {
			cells = append(cells, [2]int{r, c})
		}
		return cells
	}

	dirs := [][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		cells := collect(d[0], d[1])
		if len(cells) >= connect4ToWin {
			return true, cells
		}
	}

	return false, nil
}

// connect4WouldWin checks if placing a piece in col would result in a win for player
func connect4WouldWin(game *connect4Game, col int, player int) bool {
	row := -1
	for r := game.rows - 1; r >= 0; r-- {
		if game.board[r][col] == 0 {
			row = r
			break
		}
	}
	if row == -1 {
		return false
	}
	game.board[row][col] = player
	res, _ := connect4CheckWin(game, row, col)
	game.board[row][col] = 0
	return res
}

// connect4IsColumnFull checks if a column is full
func connect4IsColumnFull(game *connect4Game, col int) bool {
	return game.board[0][col] != 0
}

// connect4IsBoardFull checks if the entire board is full
func connect4IsBoardFull(game *connect4Game) bool {
	for col := 0; col < game.cols; col++ {
		if !connect4IsColumnFull(game, col) {
			return false
		}
	}
	return true
}

// ===========================
// AI Logic
// ===========================

// connect4MakeAIMove executes an AI move based on the configured difficulty
func connect4MakeAIMove(client *bot.Client, game *connect4Game, gameID string) {
	activeConnect4GamesMu.Lock()
	defer activeConnect4GamesMu.Unlock()

	if game.gameOver || game.currentTurn != game.aiPlayerNum {
		return
	}

	var col int
	switch game.aiDifficulty {
	case connect4Easy:
		col = connect4AIRandomMove(game)
	case connect4Normal:
		col = connect4AINormalMove(game)
	case connect4Hard:
		col = connect4AIHardMove(game)
	}

	statusMsg := connect4MakeMove(game, col)

	builder := connect4BuildMessage(game, gameID, statusMsg)
	if client != nil {
		_, _ = client.Rest.UpdateMessage(game.channelID, game.messageID, discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			SetComponents(builder.Components...).
			Build())

		if !game.gameOver && game.timerEnabled {
			connect4StartTimer(client, game, gameID, game.moveCount)
		}
	}
}

// connect4AIRandomMove selects a random valid column (Easy difficulty)
func connect4AIRandomMove(game *connect4Game) int {
	var valid []int
	for c := 0; c < game.cols; c++ {
		if !connect4IsColumnFull(game, c) {
			valid = append(valid, c)
		}
	}
	if len(valid) == 0 {
		return 0
	}
	return valid[rand.Intn(len(valid))]
}

// connect4AINormalMove tries to win, then block, then random (Normal difficulty)
func connect4AINormalMove(game *connect4Game) int {
	ai := game.aiPlayerNum
	opponent := 3 - ai

	for c := 0; c < game.cols; c++ {
		if !connect4IsColumnFull(game, c) && connect4WouldWin(game, c, ai) {
			return c
		}
	}
	for c := 0; c < game.cols; c++ {
		if !connect4IsColumnFull(game, c) && connect4WouldWin(game, c, opponent) {
			return c
		}
	}
	return connect4AIRandomMove(game)
}

func connect4AIHardMove(game *connect4Game) int {
	ai := game.aiPlayerNum
	opponent := 3 - ai

	for c := 0; c < game.cols; c++ {
		if !connect4IsColumnFull(game, c) && connect4WouldWin(game, c, ai) {
			return c
		}
	}

	for c := 0; c < game.cols; c++ {
		if !connect4IsColumnFull(game, c) && connect4WouldWin(game, c, opponent) {
			return c
		}
	}

	bestScore := -1000000
	bestCol := -1

	cols := make([]int, game.cols)
	for i := range cols {
		cols[i] = i
	}
	rand.Shuffle(len(cols), func(i, j int) { cols[i], cols[j] = cols[j], cols[i] })

	for _, c := range cols {
		if connect4IsColumnFull(game, c) {
			continue
		}

		row := -1
		for r := game.rows - 1; r >= 0; r-- {
			if game.board[r][c] == 0 {
				row = r
				break
			}
		}

		if row > 0 {
			game.board[row][c] = ai
			if connect4WouldWin(game, c, opponent) {
				game.board[row][c] = 0
				continue
			}
			game.board[row][c] = 0
		}
		game.board[row][c] = ai
		score := connect4ScorePosition(game, ai)
		game.board[row][c] = 0

		if score > bestScore {
			bestScore = score
			bestCol = c
		}
	}

	if bestCol != -1 {
		return bestCol
	}

	return connect4AIRandomMove(game)
}

func connect4ScorePosition(game *connect4Game, player int) int {
	score := 0

	centerCol := game.cols / 2
	for r := 0; r < game.rows; r++ {
		if game.board[r][centerCol] == player {
			score += 3
		}
	}

	for r := 0; r < game.rows; r++ {
		for c := 0; c < game.cols-3; c++ {
			window := []int{game.board[r][c], game.board[r][c+1], game.board[r][c+2], game.board[r][c+3]}
			score += connect4EvaluateWindow(window, player)
		}
	}
	for c := 0; c < game.cols; c++ {
		for r := 0; r < game.rows-3; r++ {
			window := []int{game.board[r][c], game.board[r+1][c], game.board[r+2][c], game.board[r+3][c]}
			score += connect4EvaluateWindow(window, player)
		}
	}
	for r := 0; r < game.rows-3; r++ {
		for c := 0; c < game.cols-3; c++ {
			window := []int{game.board[r][c], game.board[r+1][c+1], game.board[r+2][c+2], game.board[r+3][c+3]}
			score += connect4EvaluateWindow(window, player)
		}
	}
	for r := 0; r < game.rows-3; r++ {
		for c := 0; c < game.cols-3; c++ {
			window := []int{game.board[r+3][c], game.board[r+2][c+1], game.board[r+1][c+2], game.board[r][c+3]}
			score += connect4EvaluateWindow(window, player)
		}
	}

	return score
}

func connect4EvaluateWindow(window []int, player int) int {
	score := 0
	pieceCount := 0
	emptyCount := 0
	oppCount := 0
	opponent := 3 - player

	for _, cell := range window {
		switch cell {
		case player:
			pieceCount++
		case 0:
			emptyCount++
		case opponent:
			oppCount++
		}
	}

	if pieceCount == 4 {
		score += 100
	} else if pieceCount == 3 && emptyCount == 1 {
		score += 5
	} else if pieceCount == 2 && emptyCount == 2 {
		score += 2
	}

	if oppCount == 3 && emptyCount == 1 {
		score -= 4
	}

	return score
}

// ===========================
// UI Utilities
// ===========================

func connect4GetHeader(cols int) string {
	var sb strings.Builder
	for i := range cols {
		sb.WriteString(connect4ColumnEmojis[i])
	}
	return sb.String()
}

func connect4DisableInteractive(comp discord.LayoutComponent) discord.LayoutComponent {
	switch c := comp.(type) {
	case discord.ContainerComponent:
		for i, sub := range c.Components {
			c.Components[i] = connect4DisableSub(sub)
		}
		return c
	case *discord.ContainerComponent:
		for i, sub := range c.Components {
			c.Components[i] = connect4DisableSub(sub)
		}
		return c
	case discord.ActionRowComponent:
		for i, inter := range c.Components {
			c.Components[i] = connect4DisableInter(inter)
		}
		return c
	case *discord.ActionRowComponent:
		for i, inter := range c.Components {
			c.Components[i] = connect4DisableInter(inter)
		}
		return c
	}
	return comp
}

func connect4DisableSub(sub discord.ContainerSubComponent) discord.ContainerSubComponent {
	switch s := sub.(type) {
	case discord.ActionRowComponent:
		for i, inter := range s.Components {
			s.Components[i] = connect4DisableInter(inter)
		}
		return s
	case *discord.ActionRowComponent:
		for i, inter := range s.Components {
			s.Components[i] = connect4DisableInter(inter)
		}
		return s
	}
	return sub
}

func connect4DisableInter(inter discord.InteractiveComponent) discord.InteractiveComponent {
	switch i := inter.(type) {
	case discord.ButtonComponent:
		i.Disabled = true
		return i
	case *discord.ButtonComponent:
		i.Disabled = true
		return i
	}
	return inter
}

// ===========================
// Checkers Constants & Types
// ===========================

const (
	checkersRows = 8
	checkersCols = 8

	checkersEmpty         = "⬛"
	checkersWhiteTile     = "⬜"
	checkersCorner        = "⏺️"
	checkersTarget        = "🔲"
	checkersP1Piece       = "🔴"
	checkersP2Piece       = "🔵"
	checkersP1King        = "❤️"
	checkersP2King        = "💙"
	checkersP1Highlight   = "🟥"
	checkersP2Highlight   = "🟦"
	checkersStatusTurn    = "**<@%d>'s Turn** %s"
	checkersStatusWin     = "**<@%d> Lost 💩 - <@%d> Won! 🎉**"
	checkersStatusDraw    = "**<@%d> and <@%d> ended with a Draw!**"
	checkersStatusForfeit = "**<@%d> Forfeited 🛑 - <@%d> Won! 🎉**"
)

type CheckersPieceType int

const (
	PieceNone CheckersPieceType = iota
	PieceP1
	PieceP2
	PieceP1King
	PieceP2King
)

type CheckersGame struct {
	board         [checkersRows][checkersCols]CheckersPieceType
	player1ID     snowflake.ID
	player2ID     snowflake.ID
	isAI          bool
	aiDifficulty  string
	aiPlayerNum   int
	colorVariant  GameColorVariant
	currentTurn   int
	gameOver      bool
	winner        int
	moveCount     int
	lastMoveTime  time.Time
	messageID     snowflake.ID
	channelID     snowflake.ID
	selectedPiece *[2]int
	lastMoveDest  *[2]int
}

var (
	checkersColumnEmojis = []string{"🇦\u200b", "🇧\u200b", "🇨\u200b", "🇩\u200b", "🇪\u200b", "🇫\u200b", "🇬\u200b", "🇭\u200b"}
	checkersRowEmojis    = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣"}
)

// ===========================
// Checkers Logic - Core
// ===========================

func NewCheckersGame(p1, p2 snowflake.ID, isAI bool, aiPlayerNum int, difficulty string) *CheckersGame {
	variant := VariantStandard
	if rand.Intn(2) == 1 {
		variant = VariantInverted
	}
	game := &CheckersGame{
		player1ID:    p1,
		player2ID:    p2,
		isAI:         isAI,
		aiPlayerNum:  aiPlayerNum,
		aiDifficulty: difficulty,
		colorVariant: variant,
		currentTurn:  1,
		lastMoveTime: time.Now(),
	}
	game.InitBoard()
	return game
}

func (g *CheckersGame) InitBoard() {
	for r := range checkersRows {
		for c := range checkersCols {
			if (r+c)%2 == 1 {
				if r < 3 {
					g.board[r][c] = PieceP2
				} else if r > 4 {
					g.board[r][c] = PieceP1
				} else {
					g.board[r][c] = PieceNone
				}
			} else {
				g.board[r][c] = PieceNone
			}
		}
	}
}

// ===========================
// Checkers Interaction Handlers
// ===========================

func HandlePlayCheckers(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	defer func() {
		if r := recover(); r != nil {
			LogError("Panic in HandlePlayCheckers: %v", r)
			fmt.Printf("%s\n", debug.Stack())
		}
	}()

	var opponentID *snowflake.ID
	isAI := true
	var p2 snowflake.ID

	if opponent, ok := data.OptUser("opponent"); ok {
		opponentID = &opponent.ID
		isAI = opponent.Bot
	}

	difficulty := "normal"
	if diff, ok := data.OptString("difficulty"); ok {
		difficulty = diff
	}

	appID := event.ApplicationID()
	p1 := event.User().ID

	userActiveGameMu.Lock()
	if gid, ok := userActiveGame[p1]; ok {
		userActiveGameMu.Unlock()
		event.CreateMessage(discord.NewMessageCreateBuilder().SetContent(fmt.Sprintf("You are already in a game! (ID: %s)", gid)).SetEphemeral(true).Build())
		return
	}
	if opponentID != nil && *opponentID != appID {
		if gid, ok := userActiveGame[*opponentID]; ok {
			userActiveGameMu.Unlock()
			event.CreateMessage(discord.NewMessageCreateBuilder().SetContent(fmt.Sprintf("<@%d> is already in a game! (ID: %s)", *opponentID, gid)).SetEphemeral(true).Build())
			return
		}
	}

	cid := event.Channel().ID()
	gameID := fmt.Sprintf("checkers_%d_%d", cid, time.Now().UnixNano())

	userActiveGame[p1] = gameID
	if opponentID != nil && *opponentID != appID {
		userActiveGame[*opponentID] = gameID
	}
	userActiveGameMu.Unlock()

	if opponentID != nil {
		p2 = *opponentID
		if p2 == appID {
			isAI = true
		} else {
			isAI = false
		}
	} else {
		p2 = appID
		isAI = true
	}

	var player1, player2 snowflake.ID
	if rand.Intn(2) == 0 {
		player1 = p1
		player2 = p2
	} else {
		player1 = p2
		player2 = p1
	}

	aiPlayerNum := 0
	if isAI {
		if player1 == appID || (opponentID != nil && player1 == *opponentID && isAI) {
			aiPlayerNum = 1
		} else {
			aiPlayerNum = 2
		}
	}

	game := NewCheckersGame(player1, player2, isAI, aiPlayerNum, difficulty)

	activeCheckersGamesMu.Lock()
	activeCheckersGames[gameID] = game
	activeCheckersGamesMu.Unlock()

	msg := CheckersBuildMessage(game, gameID, "")
	if err := event.CreateMessage(msg); err != nil {
		LogError("Failed to send checkers message: %v", err)
		activeCheckersGamesMu.Lock()
		delete(activeCheckersGames, gameID)
		activeCheckersGamesMu.Unlock()

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
		return
	}

	resp, err := event.Client().Rest.GetInteractionResponse(event.ApplicationID(), event.Token())
	if err == nil && resp != nil {
		game.messageID = resp.ID
		game.channelID = cid
	} else {
		LogError("Failed to fetch interaction response: %v", err)
	}

	if game.isAI && game.aiPlayerNum == 1 {
		time.AfterFunc(1*time.Second, func() {
			CheckersMakeAIMove(event.Client(), game, gameID)
		})
	}
}

func HandleCheckersInteraction(event *events.ComponentInteractionCreate) {
	defer func() {
		if r := recover(); r != nil {
			LogError("Panic in HandleCheckersInteraction: %v", r)
			fmt.Printf("%s\n", debug.Stack())
		}
	}()

	parts := strings.Split(event.Data.CustomID(), ":")
	if len(parts) < 3 {
		return
	}

	gameID := parts[1]
	action := parts[2]

	activeCheckersGamesMu.Lock()
	game, exists := activeCheckersGames[gameID]
	if !exists {
		activeCheckersGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("Game not found or expired.").SetEphemeral(true).Build())
		return
	}

	userID := event.User().ID
	if userID != game.player1ID && userID != game.player2ID {
		activeCheckersGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("You are not part of this game.").SetEphemeral(true).Build())
		return
	}

	isP1 := userID == game.player1ID
	if (game.currentTurn == 1 && !isP1) || (game.currentTurn == 2 && isP1) && action != "forfeit" {
		activeCheckersGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("It's not your turn!").SetEphemeral(true).Build())
		return
	}

	switch action {
	case "select_piece":
		values := event.StringSelectMenuInteractionData().Values
		if len(values) > 0 {
			coords := strings.Split(values[0], ",")
			r, _ := strconv.Atoi(coords[0])
			c, _ := strconv.Atoi(coords[1])
			game.selectedPiece = &[2]int{r, c}
		}
	case "move_to":
		values := event.StringSelectMenuInteractionData().Values
		if len(values) > 0 {
			coords := strings.Split(values[0], ",")
			r, _ := strconv.Atoi(coords[0])
			c, _ := strconv.Atoi(coords[1])
			if game.selectedPiece != nil {
				CheckersMakeMove(game, game.selectedPiece[0], game.selectedPiece[1], r, c)
				game.selectedPiece = nil
				game.currentTurn = 3 - game.currentTurn
				if CheckersCheckWin(game) {
					game.gameOver = true
					userActiveGameMu.Lock()
					delete(userActiveGame, game.player1ID)
					delete(userActiveGame, game.player2ID)
					userActiveGameMu.Unlock()
				}
			}
		}
	case "cancel_select":
		game.selectedPiece = nil
	case "forfeit":
		game.gameOver = true
		game.winner = 3 - game.currentTurn
		if isP1 {
			game.winner = 2
		} else {
			game.winner = 1
		}
		statusMsg := fmt.Sprintf(checkersStatusForfeit, userID, game.winner)
		if userID == game.player1ID {
			statusMsg = fmt.Sprintf(checkersStatusForfeit, game.player1ID, game.player2ID)
		} else {
			statusMsg = fmt.Sprintf(checkersStatusForfeit, game.player2ID, game.player1ID)
		}

		msg := CheckersBuildMessage(game, gameID, statusMsg)
		event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			SetComponents(msg.Components...).
			Build())

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
		return

	case "claim_win":
		if !CheckersIsHopeless(game) {
			event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("The AI is not in a hopeless position yet!").SetEphemeral(true).Build())
			return
		}

		game.gameOver = true
		game.winner = 1
		if userID == game.player2ID {
			game.winner = 2
		}

		statusMsg := fmt.Sprintf("**<@%d> Claimed Victory! 🏆**", userID)
		msg := CheckersBuildMessage(game, gameID, statusMsg)

		event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			SetComponents(msg.Components...).
			Build())

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
		return

	}

	msg := CheckersBuildMessage(game, gameID, "")
	activeCheckersGamesMu.Unlock()

	_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
		SetIsComponentsV2(true).
		SetComponents(msg.Components...).
		Build())

	if !game.gameOver && game.isAI && game.currentTurn == game.aiPlayerNum {
		time.AfterFunc(1*time.Second, func() {
			CheckersMakeAIMove(event.Client(), game, gameID)
		})
	}
}

// ===========================
// Checkers Logic - Mechanics
// ===========================

func CheckersMakeMove(g *CheckersGame, r1, c1, r2, c2 int) {
	piece := g.board[r1][c1]
	g.board[r2][c2] = piece
	g.board[r1][c1] = PieceNone
	g.lastMoveDest = &[2]int{r2, c2}

	dr := r2 - r1
	dc := c2 - c1
	if dr*dr > 1 {
		midR := r1 + dr/2
		midC := c1 + dc/2
		g.board[midR][midC] = PieceNone
	}

	if piece == PieceP1 && r2 == 0 {
		g.board[r2][c2] = PieceP1King
	} else if piece == PieceP2 && r2 == checkersRows-1 {
		g.board[r2][c2] = PieceP2King
	}
}

func CheckersCheckWin(g *CheckersGame) bool {
	p1Count, p2Count := 0, 0
	for r := range checkersRows {
		for c := range checkersCols {
			p := g.board[r][c]
			if p == PieceP1 || p == PieceP1King {
				p1Count++
			}
			if p == PieceP2 || p == PieceP2King {
				p2Count++
			}
		}
	}

	if p1Count == 0 {
		g.winner = 2
		return true
	}
	if p2Count == 0 {
		g.winner = 1
		return true
	}

	validMoves1 := CheckersGetValidMoves(g, 1)
	if len(validMoves1) == 0 {
		g.winner = 2
		return true
	}
	validMoves2 := CheckersGetValidMoves(g, 2)
	if len(validMoves2) == 0 {
		g.winner = 1
		return true
	}

	return false
}

// GetValidMoves returns a map of "r,c" -> list of valid target "r,c"
func CheckersGetValidMoves(g *CheckersGame, player int) map[string][][2]int {
	moves := make(map[string][][2]int)

	for r := range checkersRows {
		for c := range checkersCols {
			p := g.board[r][c]
			isP1 := p == PieceP1 || p == PieceP1King
			isP2 := p == PieceP2 || p == PieceP2King

			if (player == 1 && isP1) || (player == 2 && isP2) {
				var dirs [][2]int
				if p == PieceP1 {
					dirs = [][2]int{{-1, -1}, {-1, 1}}
				}
				if p == PieceP2 {
					dirs = [][2]int{{1, -1}, {1, 1}}
				}
				if p == PieceP1King || p == PieceP2King {
					dirs = [][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}}
				}

				for _, d := range dirs {
					targetR, targetC := r+d[0], c+d[1]
					if CheckersIsValidPos(targetR, targetC) && g.board[targetR][targetC] == PieceNone {
						k := fmt.Sprintf("%d,%d", r, c)
						moves[k] = append(moves[k], [2]int{targetR, targetC})
					}

					jumpR, jumpC := r+2*d[0], c+2*d[1]
					if CheckersIsValidPos(jumpR, jumpC) && g.board[jumpR][jumpC] == PieceNone {
						midR, midC := r+d[0], c+d[1]
						midP := g.board[midR][midC]
						isOpponent := false
						if player == 1 && (midP == PieceP2 || midP == PieceP2King) {
							isOpponent = true
						}
						if player == 2 && (midP == PieceP1 || midP == PieceP1King) {
							isOpponent = true
						}

						if isOpponent {
							k := fmt.Sprintf("%d,%d", r, c)
							moves[k] = append(moves[k], [2]int{jumpR, jumpC})
						}
					}
				}
			}
		}
	}
	return moves
}

func CheckersIsValidPos(r, c int) bool {
	return r >= 0 && r < checkersRows && c >= 0 && c < checkersCols
}

// CheckersIsHopeless checks if the AI is in a hopeless position
func CheckersIsHopeless(game *CheckersGame) bool {
	if !game.isAI {
		return false
	}

	aiPiece := PieceP2
	aiKingPiece := PieceP2King
	playerPiece := PieceP1
	playerKingPiece := PieceP1King

	if game.aiPlayerNum == 1 {
		aiPiece = PieceP1
		aiKingPiece = PieceP1King
		playerPiece = PieceP2
		playerKingPiece = PieceP2King
	}

	aiCount := 0
	aiKingCount := 0
	playerCount := 0
	playerKingCount := 0

	for r := 0; r < checkersRows; r++ {
		for c := 0; c < checkersCols; c++ {
			p := game.board[r][c]
			switch p {
			case aiPiece:
				aiCount++
			case aiKingPiece:
				aiKingCount++
			case playerPiece:
				playerCount++
			case playerKingPiece:
				playerKingCount++
			}
		}
	}

	totalAI := aiCount + aiKingCount
	totalPlayer := playerCount + playerKingCount

	if aiKingCount == 0 && playerKingCount > 0 && totalAI < totalPlayer-1 {
		return true
	}
	if totalAI <= 2 && totalPlayer >= 3 {
		return true
	}

	return false
}

// ===========================
// Checkers Rendering & Helpers
// ===========================

func CheckersBuildMessage(game *CheckersGame, gameID string, statusMsg string) discord.MessageCreate {
	var sb strings.Builder

	// Determine visual mapping based on randomization
	p1Piece, p2Piece := checkersP1Piece, checkersP2Piece
	p1King, p2King := checkersP1King, checkersP2King
	p1Highlight, p2Highlight := checkersP1Highlight, checkersP2Highlight

	if game.colorVariant == VariantInverted {
		p1Piece, p2Piece = checkersP2Piece, checkersP1Piece
		p1King, p2King = checkersP2King, checkersP1King
		p1Highlight, p2Highlight = checkersP2Highlight, checkersP1Highlight
	}

	reverse := false
	if game.currentTurn == 2 {
		reverse = true
	}

	getHeader := func(rev bool) string {
		var hsb strings.Builder
		if !rev {
			for i := 0; i < checkersCols; i++ {
				hsb.WriteString(checkersColumnEmojis[i])
			}
		} else {
			for i := checkersCols - 1; i >= 0; i-- {
				hsb.WriteString(checkersColumnEmojis[i])
			}
		}
		return hsb.String()
	}

	headerStr := checkersCorner + getHeader(reverse) + checkersCorner + "\n"
	sb.WriteString(headerStr)

	rStart, rEnd, rStep := 0, checkersRows, 1
	cStart, cEnd, cStep := 0, checkersCols, 1

	if reverse {
		rStart, rEnd, rStep = checkersRows-1, -1, -1
		cStart, cEnd, cStep = checkersCols-1, -1, -1
	}

	for r := rStart; r != rEnd; r += rStep {
		sb.WriteString(checkersRowEmojis[r])
		for c := cStart; c != cEnd; c += cStep {
			p := game.board[r][c]

			isTarget := false
			isSelected := (game.selectedPiece != nil && game.selectedPiece[0] == r && game.selectedPiece[1] == c)
			isLastMove := (game.lastMoveDest != nil && game.lastMoveDest[0] == r && game.lastMoveDest[1] == c)

			if game.selectedPiece != nil {
				k := fmt.Sprintf("%d,%d", game.selectedPiece[0], game.selectedPiece[1])
				validMoves := CheckersGetValidMoves(game, game.currentTurn)
				if targets, ok := validMoves[k]; ok {
					for _, t := range targets {
						if t[0] == r && t[1] == c {
							isTarget = true
							break
						}
					}
				}
			}

			if isSelected || isLastMove {
				switch p {
				case PieceP1, PieceP1King:
					sb.WriteString(p1Highlight)
				case PieceP2, PieceP2King:
					sb.WriteString(p2Highlight)
				default:
					if (r+c)%2 == 1 {
						sb.WriteString(checkersEmpty)
					} else {
						sb.WriteString(checkersWhiteTile)
					}
				}
			} else if isTarget {
				sb.WriteString(checkersTarget)
			} else {
				switch p {
				case PieceNone:
					if (r+c)%2 == 1 {
						sb.WriteString(checkersEmpty)
					} else {
						sb.WriteString(checkersWhiteTile)
					}
				case PieceP1:
					sb.WriteString(p1Piece)
				case PieceP2:
					sb.WriteString(p2Piece)
				case PieceP1King:
					sb.WriteString(p1King)
				case PieceP2King:
					sb.WriteString(p2King)
				}
			}
		}
		sb.WriteString(checkersRowEmojis[r] + "\n")
	}
	sb.WriteString(headerStr)

	p1Count, p2Count := 0, 0
	for r := range checkersRows {
		for c := range checkersCols {
			p := game.board[r][c]
			if p == PieceP1 || p == PieceP1King {
				p1Count++
			}
			if p == PieceP2 || p == PieceP2King {
				p2Count++
			}
		}
	}
	scoreStr := fmt.Sprintf("-# %s <@%d>: **%d** | %s <@%d>: **%d**",
		p1Piece, game.player1ID, p1Count,
		p2Piece, game.player2ID, p2Count)

	var statusSB strings.Builder
	if game.gameOver {
		if statusMsg != "" {
			statusSB.WriteString(statusMsg)
		} else if game.winner == 0 {
			statusSB.WriteString(fmt.Sprintf(checkersStatusDraw, game.player1ID, game.player2ID))
		} else {
			winnerID := game.player1ID
			loserID := game.player2ID
			if game.winner == 2 {
				winnerID = game.player2ID
				loserID = game.player1ID
			}
			statusSB.WriteString(fmt.Sprintf(checkersStatusWin, loserID, winnerID))
		}
	} else {
		currentPlayer := game.player1ID
		statusIcon := p1Piece
		hasKing := false

		checkType := PieceP1King
		if game.currentTurn == 2 {
			currentPlayer = game.player2ID
			statusIcon = p2Piece
			checkType = PieceP2King
		}

		for r := range checkersRows {
			for c := range checkersCols {
				if game.board[r][c] == checkType {
					hasKing = true
					break
				}
			}
			if hasKing {
				break
			}
		}

		if hasKing {
			if game.currentTurn == 1 {
				statusIcon = p1King
			} else {
				statusIcon = p2King
			}
		}
		statusSB.WriteString(fmt.Sprintf(checkersStatusTurn, currentPlayer, statusIcon))

		if statusMsg != "" {
			statusSB.WriteString("\n" + statusMsg)
		}
	}

	var layoutComponents []discord.LayoutComponent
	layoutComponents = append(layoutComponents, discord.NewTextDisplay(sb.String()))
	layoutComponents = append(layoutComponents, discord.NewTextDisplay(scoreStr))

	if !game.gameOver {
		validMoves := CheckersGetValidMoves(game, game.currentTurn)
		selectedKey := ""
		if game.selectedPiece != nil {
			selectedKey = fmt.Sprintf("%d,%d", game.selectedPiece[0], game.selectedPiece[1])
		}

		var pieceOptions []discord.StringSelectMenuOption
		for k, targets := range validMoves {
			if len(targets) > 0 {
				if selectedKey != "" && k == selectedKey {
					continue
				}

				coords := strings.Split(k, ",")
				r, _ := strconv.Atoi(coords[0])
				c, _ := strconv.Atoi(coords[1])
				label := fmt.Sprintf("Row %d, Col %c", r+1, 'A'+c)
				val := k
				pieceOptions = append(pieceOptions, discord.NewStringSelectMenuOption(label, val))
			}
		}
		sort.Slice(pieceOptions, func(i, j int) bool { return pieceOptions[i].Label < pieceOptions[j].Label })

		if game.selectedPiece != nil {
			k := fmt.Sprintf("%d,%d", game.selectedPiece[0], game.selectedPiece[1])
			targets := validMoves[k]
			var destOptions []discord.StringSelectMenuOption
			for _, t := range targets {
				label := fmt.Sprintf("To Row %d, Col %c", t[0]+1, 'A'+t[1])
				val := fmt.Sprintf("%d,%d", t[0], t[1])
				destOptions = append(destOptions, discord.NewStringSelectMenuOption(label, val))
			}
			if len(destOptions) > 0 {
				menu := discord.NewStringSelectMenu(fmt.Sprintf("checkers:%s:move_to", gameID), "Select destination...", destOptions...)
				layoutComponents = append(layoutComponents, discord.NewActionRow(menu))
			}
		}

		if len(pieceOptions) > 0 {
			if len(pieceOptions) > 25 {
				pieceOptions = pieceOptions[:25]
			}
			placeholder := "Select a piece to move..."
			if game.selectedPiece != nil {
				r, c := game.selectedPiece[0], game.selectedPiece[1]
				placeholder = fmt.Sprintf("Selected: Row %d, Col %c", r+1, 'A'+c)
			}
			menu := discord.NewStringSelectMenu(fmt.Sprintf("checkers:%s:select_piece", gameID), placeholder, pieceOptions...)
			layoutComponents = append(layoutComponents, discord.NewActionRow(menu))
		}

		var utilityRow []discord.InteractiveComponent
		utilityRow = append(utilityRow, discord.NewButton(discord.ButtonStyleDanger, "Forfeit", fmt.Sprintf("checkers:%s:forfeit", gameID), "", 0))

		if CheckersIsHopeless(game) {
			utilityRow = append(utilityRow, discord.NewButton(discord.ButtonStyleSuccess, "Claim Win", fmt.Sprintf("checkers:%s:claim_win", gameID), "", 0))
		}

		layoutComponents = append(layoutComponents, discord.NewActionRow(utilityRow...))
	}

	return discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewTextDisplay(statusSB.String()),
		).
		AddComponents(layoutComponents...).
		Build()
}

// ===========================
// Checkers AI

// CheckersAIMove represents a possible move for the AI
type CheckersAIMove struct {
	r1, c1, r2, c2 int
	isJump         bool
}

func CheckersMakeAIMove(client *bot.Client, game *CheckersGame, gameID string) {
	activeCheckersGamesMu.Lock()
	defer activeCheckersGamesMu.Unlock()

	if game.gameOver || game.currentTurn != game.aiPlayerNum || !game.isAI {
		return
	}

	validMoves := CheckersGetValidMoves(game, game.aiPlayerNum)
	if len(validMoves) == 0 {
		game.gameOver = true
		game.winner = 3 - game.aiPlayerNum

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()

		msg := CheckersBuildMessage(game, gameID, "AI has no moves!")
		client.Rest.UpdateMessage(game.channelID, game.messageID, discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			SetComponents(msg.Components...).
			Build())
		return
	}

	var candidateMoves []CheckersAIMove

	for k, targets := range validMoves {
		coords := strings.Split(k, ",")
		r1, _ := strconv.Atoi(coords[0])
		c1, _ := strconv.Atoi(coords[1])
		for _, t := range targets {
			m := CheckersAIMove{r1: r1, c1: c1, r2: t[0], c2: t[1]}
			if (t[0]-r1)*(t[0]-r1) > 1 {
				m.isJump = true
			}
			candidateMoves = append(candidateMoves, m)
		}
	}

	if len(candidateMoves) > 0 {
		var jumpMoves []CheckersAIMove
		for _, m := range candidateMoves {
			if m.isJump {
				jumpMoves = append(jumpMoves, m)
			}
		}

		var possibleMoves []CheckersAIMove
		if len(jumpMoves) > 0 {
			possibleMoves = jumpMoves
		} else {
			possibleMoves = candidateMoves
		}

		var selectedMove CheckersAIMove
		switch game.aiDifficulty {
		case "easy":
			selectedMove = possibleMoves[rand.Intn(len(possibleMoves))]
		case "normal":
			selectedMove = checkersAINormalMove(game, possibleMoves)
		case "hard":
			selectedMove = checkersAIHardMove(game, possibleMoves)
		default:
			selectedMove = possibleMoves[rand.Intn(len(possibleMoves))]
		}

		CheckersMakeMove(game, selectedMove.r1, selectedMove.c1, selectedMove.r2, selectedMove.c2)
		game.lastMoveTime = time.Now()
		game.currentTurn = 3 - game.aiPlayerNum

		if CheckersCheckWin(game) {
			game.gameOver = true
			userActiveGameMu.Lock()
			delete(userActiveGame, game.player1ID)
			delete(userActiveGame, game.player2ID)
			userActiveGameMu.Unlock()
		}
	}

	msg := CheckersBuildMessage(game, gameID, "")
	client.Rest.UpdateMessage(game.channelID, game.messageID, discord.NewMessageUpdateBuilder().
		SetIsComponentsV2(true).
		SetComponents(msg.Components...).
		Build())
}

// checkersAINormalMove prioritizes promotion (Kings), then random
func checkersAINormalMove(game *CheckersGame, moves []CheckersAIMove) CheckersAIMove {
	var promoteMoves []CheckersAIMove

	targetRow := 0
	if game.aiPlayerNum == 2 {
		targetRow = checkersRows - 1
	}

	for _, m := range moves {
		if m.r2 == targetRow {
			p := game.board[m.r1][m.c1]
			isKing := (p == PieceP1King || p == PieceP2King)
			if !isKing {
				promoteMoves = append(promoteMoves, m)
			}
		}
	}

	if len(promoteMoves) > 0 {
		return promoteMoves[rand.Intn(len(promoteMoves))]
	}
	return moves[rand.Intn(len(moves))]
}

// checkersAIHardMove prioritizes Safety -> Promotion -> Random
func checkersAIHardMove(game *CheckersGame, moves []CheckersAIMove) CheckersAIMove {
	var safeMoves []CheckersAIMove

	for _, m := range moves {
		if !checkersIsVulnerable(game, m.r2, m.c2) {
			safeMoves = append(safeMoves, m)
		}
	}

	candidates := safeMoves
	if len(candidates) == 0 {
		candidates = moves
	}

	return checkersAINormalMove(game, candidates)
}

// checkersIsVulnerable checks if a piece at r,c can be jumped by the opponent
func checkersIsVulnerable(game *CheckersGame, r, c int) bool {
	opponent := 1
	if game.aiPlayerNum == 1 {
		opponent = 2
	}

	dirs := [][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}}

	for _, d := range dirs {
		attackerR, attackerC := r+d[0], c+d[1]

		if !CheckersIsValidPos(attackerR, attackerC) {
			continue
		}

		piece := game.board[attackerR][attackerC]
		if piece == PieceNone {
			continue
		}

		isOpponent := false
		isKing := (piece == PieceP1King || piece == PieceP2King)

		if opponent == 1 && (piece == PieceP1 || piece == PieceP1King) {
			isOpponent = true
		} else if opponent == 2 && (piece == PieceP2 || piece == PieceP2King) {
			isOpponent = true
		}

		if !isOpponent {
			continue
		}

		canAttackDir := false
		if isKing {
			canAttackDir = true
		} else if opponent == 1 && d[0] == 1 {
			canAttackDir = true
		} else if opponent == 2 && d[0] == -1 {
			canAttackDir = true
		}

		if canAttackDir {
			landR, landC := r-d[0], c-d[1]
			if CheckersIsValidPos(landR, landC) && game.board[landR][landC] == PieceNone {
				return true
			}
		}
	}
	return false
}

// ===========================
// Chess Game Constants & Types
// ===========================

const (
	chessRows = 8
	chessCols = 8

	chessWhiteSquare = "▫️"
	chessBlackSquare = "▪️"
	chessSelected    = "🟩"

	chessTargetWhite = "◻️"
	chessTargetBlack = "◼️"

	chessWhiteKing   = "🤟🏻"
	chessWhiteQueen  = "🖐🏻"
	chessWhiteRook   = "✊🏻"
	chessWhiteBishop = "👐🏻"
	chessWhiteKnight = "🤙🏻"
	chessWhitePawn   = "☝🏻"

	chessBlackKing   = "🤟🏿"
	chessBlackQueen  = "🖐🏿"
	chessBlackRook   = "✊🏿"
	chessBlackBishop = "👐🏿"
	chessBlackKnight = "🤙🏿"
	chessBlackPawn   = "☝🏿"

	chessStatusTurn    = "**<@%d>'s Turn** %s"
	chessStatusWin     = "**<@%d> Lost 💩 - <@%d> Won! 🎉**"
	chessStatusDraw    = "**<@%d> and <@%d> ended with a Draw!**"
	chessStatusForfeit = "**<@%d> Forfeited 🛑 - <@%d> Won! 🎉**"
)

// ChessGame represents a Chess game session
type ChessGame struct {
	game          *chess.Game
	player1ID     snowflake.ID
	player2ID     snowflake.ID
	isAI          bool
	aiDifficulty  string
	aiPlayerNum   int
	colorVariant  GameColorVariant
	currentTurn   int
	gameOver      bool
	winner        int
	messageID     snowflake.ID
	channelID     snowflake.ID
	selectedPiece *chess.Square
	lastMove      *chess.Move
	p1Icon        string
	p2Icon        string
}

func (g *ChessGame) GetPieceIcon(p chess.Piece) string {
	wKing, bKing := chessWhiteKing, chessBlackKing
	wQueen, bQueen := chessWhiteQueen, chessBlackQueen
	wRook, bRook := chessWhiteRook, chessBlackRook
	wBishop, bBishop := chessWhiteBishop, chessBlackBishop
	wKnight, bKnight := chessWhiteKnight, chessBlackKnight
	wPawn, bPawn := chessWhitePawn, chessBlackPawn

	if g.colorVariant == VariantInverted {
		wKing, bKing = chessBlackKing, chessWhiteKing
		wQueen, bQueen = chessBlackQueen, chessWhiteQueen
		wRook, bRook = chessBlackRook, chessWhiteRook
		wBishop, bBishop = chessBlackBishop, chessWhiteBishop
		wKnight, bKnight = chessBlackKnight, chessWhiteKnight
		wPawn, bPawn = chessBlackPawn, chessWhitePawn
	}

	switch p.Type() {
	case chess.Pawn:
		if p.Color() == chess.White {
			return wPawn
		}
		return bPawn
	case chess.Knight:
		if p.Color() == chess.White {
			return wKnight
		}
		return bKnight
	case chess.Bishop:
		if p.Color() == chess.White {
			return wBishop
		}
		return bBishop
	case chess.Rook:
		if p.Color() == chess.White {
			return wRook
		}
		return bRook
	case chess.Queen:
		if p.Color() == chess.White {
			return wQueen
		}
		return bQueen
	case chess.King:
		if p.Color() == chess.White {
			return wKing
		}
		return bKing
	}
	return ""
}

func NewChessGame(p1, p2 snowflake.ID, isAI bool, aiPlayerNum int, difficulty string) *ChessGame {
	variant := VariantStandard
	if rand.Intn(2) == 1 {
		variant = VariantInverted
	}
	game := &ChessGame{
		game:         chess.NewGame(),
		player1ID:    p1,
		player2ID:    p2,
		isAI:         isAI,
		aiDifficulty: difficulty,
		aiPlayerNum:  aiPlayerNum,
		colorVariant: variant,
		currentTurn:  1,
	}
	game.p1Icon = game.GetPieceIcon(chess.Piece(chess.WhiteKing))
	game.p2Icon = game.GetPieceIcon(chess.Piece(chess.BlackKing))
	return game
}

// ===========================
// Chess Interaction Handlers
// ===========================

func HandlePlayChess(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	defer func() {
		if r := recover(); r != nil {
			LogError("Panic in HandlePlayChess: %v", r)
			fmt.Printf("%s\n", debug.Stack())
		}
	}()

	var opponentID *snowflake.ID
	isAI := true
	var p2 snowflake.ID

	if opponent, ok := data.OptUser("opponent"); ok {
		opponentID = &opponent.ID
		isAI = opponent.Bot
	}

	difficulty := "normal"
	if diff, ok := data.OptString("difficulty"); ok {
		difficulty = diff
	}

	appID := event.ApplicationID()
	p1 := event.User().ID

	userActiveGameMu.Lock()
	if gid, ok := userActiveGame[p1]; ok {
		userActiveGameMu.Unlock()
		event.CreateMessage(discord.NewMessageCreateBuilder().SetContent(fmt.Sprintf("You are already in a game! (ID: %s)", gid)).SetEphemeral(true).Build())
		return
	}
	if opponentID != nil && *opponentID != appID {
		if gid, ok := userActiveGame[*opponentID]; ok {
			userActiveGameMu.Unlock()
			event.CreateMessage(discord.NewMessageCreateBuilder().SetContent(fmt.Sprintf("<@%d> is already in a game! (ID: %s)", *opponentID, gid)).SetEphemeral(true).Build())
			return
		}
	}

	cid := event.Channel().ID()
	gameID := fmt.Sprintf("chess_%d_%d", cid, time.Now().UnixNano())

	userActiveGame[p1] = gameID
	if opponentID != nil && *opponentID != appID {
		userActiveGame[*opponentID] = gameID
	}
	userActiveGameMu.Unlock()

	if opponentID != nil {
		p2 = *opponentID
		if p2 == appID {
			isAI = true
		} else {
			isAI = false
		}
	} else {
		p2 = appID
		isAI = true
	}

	var whitePlayer, blackPlayer snowflake.ID
	if rand.Intn(2) == 0 {
		whitePlayer = p1
		blackPlayer = p2
	} else {
		whitePlayer = p2
		blackPlayer = p1
	}

	aiPlayerNum := 0
	if isAI {
		if whitePlayer == appID || (opponentID != nil && whitePlayer == *opponentID && isAI) {
			aiPlayerNum = 1
		} else {
			aiPlayerNum = 2
		}
	}

	game := NewChessGame(whitePlayer, blackPlayer, isAI, aiPlayerNum, difficulty)

	activeChessGamesMu.Lock()
	activeChessGames[gameID] = game
	activeChessGamesMu.Unlock()

	msg := ChessBuildMessage(game, gameID, "")
	if err := event.CreateMessage(msg); err != nil {
		LogError("Failed to send chess message: %v", err)
		activeChessGamesMu.Lock()
		delete(activeChessGames, gameID)
		activeChessGamesMu.Unlock()

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
		return
	}

	resp, err := event.Client().Rest.GetInteractionResponse(event.ApplicationID(), event.Token())
	if err == nil && resp != nil {
		game.messageID = resp.ID
		game.channelID = cid
	}

	if game.isAI && game.aiPlayerNum == 1 {
		time.AfterFunc(1*time.Second, func() {
			ChessMakeAIMove(event.Client(), game, gameID)
		})
	}
}

func HandleChessInteraction(event *events.ComponentInteractionCreate) {
	defer func() {
		if r := recover(); r != nil {
			LogError("Panic in HandleChessInteraction: %v", r)
			fmt.Printf("%s\n", debug.Stack())
		}
	}()

	parts := strings.Split(event.Data.CustomID(), ":")
	if len(parts) < 3 {
		return
	}

	gameID := parts[1]
	action := parts[2]
	statusMsg := ""

	activeChessGamesMu.Lock()
	game, exists := activeChessGames[gameID]
	if !exists {
		activeChessGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("Game not found or expired.").SetEphemeral(true).Build())
		return
	}

	userID := event.User().ID
	if userID != game.player1ID && userID != game.player2ID {
		activeChessGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("You are not part of this game.").SetEphemeral(true).Build())
		return
	}

	isWhite := userID == game.player1ID
	isTurn := (game.currentTurn == 1 && isWhite) || (game.currentTurn == 2 && !isWhite)

	if !isTurn && action != "forfeit" {
		activeChessGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("It's not your turn!").SetEphemeral(true).Build())
		return
	}

	switch action {
	case "select_piece":
		values := event.StringSelectMenuInteractionData().Values
		if len(values) > 0 {
			sqVal, _ := strconv.Atoi(values[0])
			sq := chess.Square(sqVal)
			game.selectedPiece = &sq
		}
	case "move_to":
		values := event.StringSelectMenuInteractionData().Values
		if len(values) > 0 {
			toSqVal, _ := strconv.Atoi(values[0])
			toSq := chess.Square(toSqVal)

			if game.selectedPiece != nil {
				validMoves := game.game.ValidMoves()
				var selectedMove *chess.Move
				for _, m := range validMoves {
					if m.S1() == *game.selectedPiece && m.S2() == toSq {
						if m.Promo() != chess.NoPieceType && m.Promo() != chess.Queen {
							continue
						}
						selectedMove = &m
						break
					}
				}

				if selectedMove != nil {
					game.game.PushNotationMove(chess.UCINotation{}.Encode(game.game.Position(), selectedMove), chess.UCINotation{}, nil)

					icon := game.GetPieceIcon(game.game.Position().Board().Piece(selectedMove.S2()))
					if isWhite {
						game.p1Icon = icon
					} else {
						game.p2Icon = icon
					}

					game.lastMove = selectedMove
					game.selectedPiece = nil
					game.currentTurn = 3 - game.currentTurn

					outcome := game.game.Outcome()
					if outcome != chess.NoOutcome {
						game.gameOver = true
						switch outcome {
						case chess.WhiteWon:
							game.winner = 1
						case chess.BlackWon:
							game.winner = 2
						default:
							game.winner = 0
						}

						userActiveGameMu.Lock()
						delete(userActiveGame, game.player1ID)
						delete(userActiveGame, game.player2ID)
						userActiveGameMu.Unlock()
					}
				}
			}
		}
	case "forfeit":
		game.gameOver = true
		winnerID := game.player1ID
		loserID := game.player2ID
		if isWhite {
			game.winner = 2
			winnerID = game.player2ID
			loserID = game.player1ID
		} else {
			game.winner = 1
		}
		statusMsg = fmt.Sprintf(chessStatusForfeit, loserID, winnerID)

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
	}

	msg := ChessBuildMessage(game, gameID, statusMsg)
	activeChessGamesMu.Unlock()

	_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
		SetIsComponentsV2(true).
		SetComponents(msg.Components...).
		Build())

	if !game.gameOver && game.isAI && game.currentTurn == game.aiPlayerNum {
		time.AfterFunc(1*time.Second, func() {
			ChessMakeAIMove(event.Client(), game, gameID)
		})
	}
}

// ===========================
// Chess Rendering & Helpers
// ===========================

func ChessBuildMessage(game *ChessGame, gameID string, statusMsg string) discord.MessageCreate {
	var sb strings.Builder

	reverse := false
	if game.currentTurn == 2 {
		reverse = true
	}

	getHeader := func(rev bool) string {
		var hsb strings.Builder
		if !rev {
			for i := 0; i < chessCols; i++ {
				hsb.WriteString(checkersColumnEmojis[i])
			}
		} else {
			for i := chessCols - 1; i >= 0; i-- {
				hsb.WriteString(checkersColumnEmojis[i])
			}
		}
		return hsb.String()
	}

	headerStr := checkersCorner + getHeader(reverse) + checkersCorner + "\n"
	sb.WriteString(headerStr)

	board := game.game.Position().Board()
	validDestinations := make(map[chess.Square]bool)
	if game.selectedPiece != nil {
		for _, m := range game.game.ValidMoves() {
			if m.S1() == *game.selectedPiece {
				validDestinations[m.S2()] = true
			}
		}
	}

	rStart, rEnd, rStep := chessRows-1, -1, -1
	cStart, cEnd, cStep := 0, chessCols, 1
	if reverse {
		rStart, rEnd, rStep = 0, chessRows, 1
		cStart, cEnd, cStep = chessCols-1, -1, -1
	}

	for r := rStart; r != rEnd; r += rStep {
		sb.WriteString(checkersRowEmojis[7-r])
		for c := cStart; c != cEnd; c += cStep {
			sq := chess.Square(r*8 + c)

			bg := chessWhiteSquare
			isWhiteSquare := (r+c)%2 != 0
			if !isWhiteSquare {
				bg = chessBlackSquare
			}

			isSelected := game.selectedPiece != nil && *game.selectedPiece == sq
			isLastMoveSrc := game.lastMove != nil && game.lastMove.S1() == sq
			isLastMoveDst := game.lastMove != nil && game.lastMove.S2() == sq
			isTarget := validDestinations[sq]

			display := bg

			if isSelected {
				display = chessSelected
			} else if isTarget || isLastMoveSrc || isLastMoveDst {
				if isWhiteSquare {
					display = chessTargetWhite
				} else {
					display = chessTargetBlack
				}
			}

			p := board.Piece(sq)
			pieceIcon := game.GetPieceIcon(p)
			if pieceIcon != "" {
				display = pieceIcon
			}

			sb.WriteString(display)
		}
		sb.WriteString(checkersRowEmojis[7-r] + "\n")
	}
	sb.WriteString(headerStr)

	wCount, bCount := 0, 0
	for sq := range 64 {
		p := board.Piece(chess.Square(sq))
		if p.Color() == chess.White {
			wCount++
		} else if p.Color() == chess.Black {
			bCount++
		}
	}

	p1Disp := fmt.Sprintf("%s <@%d>: **%d**", game.p1Icon, game.player1ID, wCount)
	p2Disp := fmt.Sprintf("%s <@%d>: **%d**", game.p2Icon, game.player2ID, bCount)
	if reverse {
		p1Disp, p2Disp = p2Disp, p1Disp
	}
	scoreStr := fmt.Sprintf("-# %s | %s", p1Disp, p2Disp)

	var statusSB strings.Builder
	if game.gameOver {
		if statusMsg != "" {
			statusSB.WriteString(statusMsg)
		} else if game.winner == 0 {
			statusSB.WriteString(fmt.Sprintf(chessStatusDraw, game.player1ID, game.player2ID))
		} else {
			winnerID := game.player1ID
			loserID := game.player2ID
			if game.winner == 2 {
				winnerID = game.player2ID
				loserID = game.player1ID
			}
			statusSB.WriteString(fmt.Sprintf(chessStatusWin, loserID, winnerID))
		}
	} else {
		currentPlayer := game.player1ID
		statusIcon := game.p1Icon
		if game.currentTurn == 2 {
			currentPlayer = game.player2ID
			statusIcon = game.p2Icon
		}
		statusSB.WriteString(fmt.Sprintf(chessStatusTurn, currentPlayer, statusIcon))

		if statusMsg != "" {
			statusSB.WriteString("\n" + statusMsg)
		}
		if game.game.Position().Status() == chess.Checkmate {
			statusSB.WriteString(" (CHECKMATE)")
		} else if game.lastMove != nil && game.lastMove.HasTag(chess.Check) {
			statusSB.WriteString(" (CHECK)")
		}
	}

	layoutComponents := []discord.LayoutComponent{
		discord.NewTextDisplay(sb.String()),
		discord.NewTextDisplay(scoreStr),
	}

	if !game.gameOver {
		moves := game.game.ValidMoves()
		seen := make(map[chess.Square]bool)
		var pieceOpts []discord.StringSelectMenuOption

		selectedSq := chess.NoSquare
		if game.selectedPiece != nil {
			selectedSq = *game.selectedPiece
		}

		for _, m := range moves {
			s1 := m.S1()
			if !seen[s1] {
				seen[s1] = true
				if s1 == selectedSq {
					continue
				}

				p := board.Piece(s1)
				pieceIcon := game.GetPieceIcon(p)
				label := fmt.Sprintf("%s %c%d", pieceIcon, 'A'+s1.File(), 8-s1.Rank())
				val := strconv.Itoa(int(s1))
				pieceOpts = append(pieceOpts, discord.NewStringSelectMenuOption(label, val))
			}
		}

		sort.Slice(pieceOpts, func(i, j int) bool { return pieceOpts[i].Label < pieceOpts[j].Label })
		if len(pieceOpts) > 25 {
			pieceOpts = pieceOpts[:25]
		}

		if game.selectedPiece != nil {
			var destOpts []discord.StringSelectMenuOption
			for _, m := range moves {
				if m.S1() == *game.selectedPiece {
					if m.Promo() != chess.NoPieceType && m.Promo() != chess.Queen {
						continue
					}
					s2 := m.S2()
					label := fmt.Sprintf("To %c%d", 'A'+s2.File(), 8-s2.Rank())
					if m.HasTag(chess.Capture) {
						label += " (Capture)"
					}
					val := strconv.Itoa(int(s2))
					destOpts = append(destOpts, discord.NewStringSelectMenuOption(label, val))
				}
			}
			if len(destOpts) > 25 {
				destOpts = destOpts[:25]
			}
			if len(destOpts) > 0 {
				menu := discord.NewStringSelectMenu(fmt.Sprintf("chess:%s:move_to", gameID), "Select Destination...", destOpts...)
				layoutComponents = append(layoutComponents, discord.NewActionRow(menu))
			}
		}

		if len(pieceOpts) > 0 {
			placeholder := "Select Piece..."
			if game.selectedPiece != nil {
				s := *game.selectedPiece
				placeholder = fmt.Sprintf("Selected: %c%d", 'A'+s.File(), 8-s.Rank())
			}
			menu := discord.NewStringSelectMenu(fmt.Sprintf("chess:%s:select_piece", gameID), placeholder, pieceOpts...)
			layoutComponents = append(layoutComponents, discord.NewActionRow(menu))
		}

		row := discord.NewActionRow(
			discord.NewButton(discord.ButtonStyleDanger, "Forfeit", fmt.Sprintf("chess:%s:forfeit", gameID), "", 0),
		)
		layoutComponents = append(layoutComponents, row)
	}

	return discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(discord.NewTextDisplay(statusSB.String())).
		AddComponents(layoutComponents...).
		Build()
}

func ChessMakeAIMove(client *bot.Client, game *ChessGame, gameID string) {
	activeChessGamesMu.Lock()
	if game.gameOver || game.currentTurn != game.aiPlayerNum {
		activeChessGamesMu.Unlock()
		return
	}

	difficulty := game.aiDifficulty
	fen := game.game.Position().String()
	moves := game.game.ValidMoves()
	activeChessGamesMu.Unlock()

	if len(moves) == 0 {
		return
	}

	var selectedMove *chess.Move
	isHardFallback := false

	if difficulty == "hard" {
		reqBody, _ := json.Marshal(map[string]string{"fen": fen})
		resp, err := http.Post("https://chess-api.com/v1", "application/json", bytes.NewBuffer(reqBody))
		if err == nil {
			defer resp.Body.Close()
			var apiResp struct {
				Move string `json:"move"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&apiResp); err == nil {
				activeChessGamesMu.Lock()
				move, err := chess.UCINotation{}.Decode(game.game.Position(), apiResp.Move)
				if err == nil {
					selectedMove = move
				}
				activeChessGamesMu.Unlock()
			}
		}

		if selectedMove == nil {
			isHardFallback = true
			difficulty = "normal"
		}
	}

	activeChessGamesMu.Lock()
	defer activeChessGamesMu.Unlock()

	if game.gameOver || game.currentTurn != game.aiPlayerNum {
		return
	}

	if selectedMove == nil {
		var captureMoves []*chess.Move
		for _, m := range moves {
			mCopy := m
			if m.HasTag(chess.Capture) {
				captureMoves = append(captureMoves, &mCopy)
			}
		}

		if len(captureMoves) > 0 && difficulty != "easy" {
			selectedMove = captureMoves[rand.Intn(len(captureMoves))]
		} else {
			mCopy := moves[rand.Intn(len(moves))]
			selectedMove = &mCopy
		}
	}

	game.game.PushNotationMove(chess.UCINotation{}.Encode(game.game.Position(), selectedMove), chess.UCINotation{}, nil)
	game.lastMove = selectedMove

	icon := game.GetPieceIcon(game.game.Position().Board().Piece(selectedMove.S2()))
	if game.aiPlayerNum == 1 {
		game.p1Icon = icon
	} else {
		game.p2Icon = icon
	}

	game.currentTurn = 3 - game.currentTurn

	outcome := game.game.Outcome()
	if outcome != chess.NoOutcome {
		game.gameOver = true
		switch outcome {
		case chess.WhiteWon:
			game.winner = 1
		case chess.BlackWon:
			game.winner = 2
		default:
			game.winner = 0
		}

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
	}

	statusMsg := ""
	if isHardFallback {
		statusMsg = "⚠️ *Hard AI service unavailable, using Normal AI.*"
	}

	msg := ChessBuildMessage(game, gameID, statusMsg)
	client.Rest.UpdateMessage(game.channelID, game.messageID, discord.NewMessageUpdateBuilder().
		SetIsComponentsV2(true).
		SetComponents(msg.Components...).
		Build())
}
