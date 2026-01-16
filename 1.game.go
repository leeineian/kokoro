package main

import (
	"fmt"
	"math/rand"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

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
							{Name: "Medium", Value: "medium"},
							{Name: "Hard", Value: "hard"},
							{Name: "Impossible", Value: "impossible"},
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
		}
	})

	RegisterComponentHandler("connect4:", c4HandleMove)
}

// ===========================
// Connect Four Game Constants & Types
// ===========================

const (
	c4ToWin   = 4
	c4P1      = "🔴"
	c4P2      = "🟡"
	c4Empty   = "⚫"
	c4Forfeit = "🛑"
	c4P1Win   = "🟥"
	c4P2Win   = "🟨"

	c4StatusTurn     = "**<@%d>'s Turn** %s"
	c4StatusDraw     = "**<@%d> and <@%d> ended with a Draw!**"
	c4StatusWin      = "**<@%d> Lost 💩 - <@%d> Won! 🎉**"
	c4StatusForfeit  = "**<@%d> Forfeited 🛑 - <@%d> Won! 🎉**"
	c4StatusTimeout  = "**<@%d> Took Too Long ⏱️ - <@%d> Won! 🎉**"
	c4StatusInactive = "**❌ `This game is no longer active.`**"
	c4StatusFull     = "**❌ `Column is full.`**"
)

var (
	c4ColumnEmojis = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟"}
)

// c4Difficulty represents the AI difficulty level for Connect Four
type c4Difficulty int

const (
	c4Easy c4Difficulty = iota
	c4Medium
	c4Hard
	c4Impossible
)

// c4Game represents a Connect Four game session with all its state
type c4Game struct {
	board         [][]int       // Game board (0=empty, 1=player1, 2=player2)
	rows          int           // Number of rows
	cols          int           // Number of columns
	player1ID     snowflake.ID  // Player 1's Discord ID
	player2ID     snowflake.ID  // Player 2's Discord ID
	isAI          bool          // Whether this is a PvAI game
	aiDifficulty  c4Difficulty  // AI difficulty level
	aiPlayerNum   int           // Which player is AI (1 or 2)
	currentTurn   int           // Current turn (1 or 2)
	gameOver      bool          // Whether the game has ended
	winner        int           // Winner (0=draw, 1=player1, 2=player2)
	winCells      [][2]int      // Coordinates of winning cells
	moveCount     int           // Total moves made
	timerEnabled  bool          // Whether turn timer is enabled
	timerDuration time.Duration // Duration for each turn
	lastMoveTime  time.Time     // Time of last move
	messageID     snowflake.ID  // Discord message ID
	channelID     snowflake.ID  // Discord channel ID
	originalP1ID  snowflake.ID  // Original player 1 ID (for replays)
	originalP2ID  snowflake.ID  // Original player 2 ID (for replays)
}

// ===========================
// Global State & Initialization
// ===========================

var (
	activeGames   = make(map[string]*c4Game)
	activeGamesMu sync.RWMutex
)

// ===========================
// Interaction Handlers
// ===========================

// handlePlayConnect4 initiates a new game session (Slash Command)
func handlePlayConnect4(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	// Add panic recovery for debugging
	defer func() {
		if r := recover(); r != nil {
			LogError("Panic in handlePlayConnect4: %v", r)
			fmt.Printf("%s\n", debug.Stack())
		}
	}()

	// Parse options
	var opponentID *snowflake.ID
	difficulty := c4Medium

	var isOpponentBot bool
	if opponent, ok := data.OptUser("opponent"); ok {
		opponentID = &opponent.ID
		isOpponentBot = opponent.Bot
	}

	if diff, ok := data.OptString("difficulty"); ok {
		switch diff {
		case "easy":
			difficulty = c4Easy
		case "medium":
			difficulty = c4Medium
		case "hard":
			difficulty = c4Hard
		case "impossible":
			difficulty = c4Impossible
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

	// Create game
	cid := event.Channel().ID()
	gameID := fmt.Sprintf("%d_%d", cid, time.Now().UnixNano())
	game := &c4Game{
		rows:          rows,
		cols:          cols,
		player1ID:     event.User().ID,
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

	// Determine if it's AI or PVP
	appID := event.ApplicationID()
	p1 := event.User().ID
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

	// Randomize who goes first
	game.originalP1ID = p1
	game.originalP2ID = p2
	if rand.Intn(2) == 0 {
		game.player1ID = p1
		game.player2ID = p2
		if game.isAI {
			game.aiPlayerNum = 2
		}
	} else {
		game.player1ID = p2
		game.player2ID = p1
		if game.isAI {
			game.aiPlayerNum = 1
		}
	}

	// Store game
	activeGamesMu.Lock()
	activeGames[gameID] = game
	activeGamesMu.Unlock()

	// Send initial board (as the interaction response)
	builder := c4BuildMessage(game, gameID, "")
	if err := event.CreateMessage(builder); err != nil {
		return
	}

	// Fetch the message ID of the interaction response to track the session
	msg, err := event.Client().Rest.GetInteractionResponse(event.ApplicationID(), event.Token())
	if err == nil && msg != nil {
		game.messageID = msg.ID
	}

	// Start timer if enabled (ONLY after messageID is known)
	if game.timerEnabled {
		c4StartTimer(event.Client(), game, gameID, 0)
	}

	// If AI's turn, trigger move
	if game.isAI && game.currentTurn == game.aiPlayerNum {
		go func() {
			time.Sleep(1 * time.Second)
			c4MakeAIMove(event.Client(), game, gameID)
		}()
	}
}

// c4StartTimer watches for turn expiry and forfeits if time runs out
func c4StartTimer(client *bot.Client, game *c4Game, gameID string, moveCount int) {
	go func() {
		time.Sleep(game.timerDuration)

		activeGamesMu.Lock()
		// Check if it's still the same game and turn
		if game.moveCount != moveCount || game.gameOver || game.messageID == 0 {
			activeGamesMu.Unlock()
			return
		}

		winnerID := game.player1ID
		loserID := game.player2ID
		if game.currentTurn == 1 {
			winnerID = game.player2ID
			loserID = game.player1ID
		}
		game.winner = (game.currentTurn % 2) + 1 // Actually turn 1 -> winner 2, turn 2 -> winner 1
		// Wait, if currentTurn is 1, winner is 2.
		if game.currentTurn == 1 {
			game.winner = 2
		} else {
			game.winner = 1
		}
		game.gameOver = true // Set game over when timeout occurs

		status := fmt.Sprintf(c4StatusTimeout, loserID, winnerID)
		builder := c4BuildMessage(game, gameID, status)
		if client != nil {
			_, _ = client.Rest.UpdateMessage(game.channelID, game.messageID, discord.NewMessageUpdateBuilder().
				SetComponents(builder.Components...).
				Build())
		}

		activeGamesMu.Unlock()
	}()
}

// c4HandleMove processes player moves and forfeits (Component Interaction)
func c4HandleMove(event *events.ComponentInteractionCreate) {
	parts := strings.Split(event.Data.CustomID(), ":")
	if len(parts) < 3 {
		event.DeferUpdateMessage()
		return
	}
	gameID := parts[1]
	action := parts[2]

	activeGamesMu.Lock()
	game, exists := activeGames[gameID]
	if !exists {
		activeGamesMu.Unlock()
		// ...
		if event.Message.ID == 0 {
			event.DeferUpdateMessage()
			return
		}

		var newComponents []discord.LayoutComponent
		for i, comp := range event.Message.Components {
			modified := c4DisableInteractive(comp)
			if i == 0 {
				if td, ok := modified.(discord.TextDisplayComponent); ok {
					modified = discord.NewTextDisplay(td.Content + "\n" + c4StatusInactive)
				} else if td, ok := modified.(*discord.TextDisplayComponent); ok {
					modified = discord.NewTextDisplay(td.Content + "\n" + c4StatusInactive)
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
		activeGamesMu.Unlock()
		event.DeferUpdateMessage()
		return
	}
	activeGamesMu.Unlock()

	// Yes/No logic for replay
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
			activeGamesMu.Lock()
			delete(activeGames, gameID)
			activeGamesMu.Unlock()

			// Generate the full message as if the game ended normally
			fullMsg := c4BuildMessage(game, gameID, "")

			var newComponents []discord.LayoutComponent

			if len(fullMsg.Components) > 0 {
				newComponents = append(newComponents, fullMsg.Components[0])
			}

			if len(fullMsg.Components) > 1 {
				var boardSub discord.ContainerSubComponent

				switch c := fullMsg.Components[1].(type) {
				case discord.ContainerComponent:
					if len(c.Components) > 0 {
						boardSub = c.Components[0]
					}
				case *discord.ContainerComponent:
					if len(c.Components) > 0 {
						boardSub = c.Components[0]
					}
				}

				if boardSub != nil {
					newComponents = append(newComponents, discord.NewContainer(boardSub))
				}
			}

			_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				SetComponents(newComponents...).
				Build())
			return
		}

		// Replay logic (Yes)
		activeGamesMu.Lock()
		// Reset board
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

		// Randomize who goes first again
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
		activeGamesMu.Unlock()

		builder := c4BuildMessage(game, gameID, "🔄 Game Restarted!")
		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetComponents(builder.Components...).
			Build())

		// Start timer if enabled
		if game.timerEnabled {
			c4StartTimer(event.Client(), game, gameID, 0)
		}

		// If AI is Player 1, trigger move
		if game.isAI && game.aiPlayerNum == 1 {
			go func() {
				time.Sleep(1 * time.Second)
				c4MakeAIMove(event.Client(), game, gameID)
			}()
		}
		return
	}

	// Forfeit logic
	if action == "forfeit" {
		userID := event.User().ID
		if userID != game.player1ID && userID != game.player2ID {
			event.CreateMessage(discord.NewMessageCreateBuilder().
				SetContent("You're not a player in this game!").
				SetEphemeral(true).
				Build())
			return
		}

		activeGamesMu.Lock()
		game.gameOver = true
		var forfeitMsg string
		if userID == game.player1ID {
			game.winner = 2
			forfeitMsg = fmt.Sprintf(c4StatusForfeit, game.player1ID, game.player2ID)
		} else {
			game.winner = 1
			forfeitMsg = fmt.Sprintf(c4StatusForfeit, game.player2ID, game.player1ID)
		}
		activeGamesMu.Unlock()

		builder := c4BuildMessage(game, gameID, forfeitMsg)
		event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetComponents(builder.Components...).
			Build())
		return
	}

	// Move logic
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

	activeGamesMu.Lock()
	if game.gameOver && action != "yes" && action != "no" {
		activeGamesMu.Unlock()
		event.DeferUpdateMessage()
		return
	}

	statusMsg := c4MakeMove(game, col)
	activeGamesMu.Unlock()

	builder := c4BuildMessage(game, gameID, statusMsg)
	event.UpdateMessage(discord.NewMessageUpdateBuilder().
		SetComponents(builder.Components...).
		Build())

	if !game.gameOver {
		// Start timer for next turn if enabled
		if game.timerEnabled {
			c4StartTimer(event.Client(), game, gameID, game.moveCount)
		}

		// If AI's turn, make AI move after a short delay
		if game.isAI && game.currentTurn == game.aiPlayerNum {
			go func() {
				time.Sleep(1 * time.Second)
				c4MakeAIMove(event.Client(), game, gameID)
			}()
		}
	}
}

// ===========================
// UI & Rendering
// ===========================

// c4BuildMessage constructs the Discord message for the current game state
func c4BuildMessage(game *c4Game, gameID string, statusMsg string) discord.MessageCreate {
	var sb strings.Builder
	header := c4GetHeader(game.cols)
	sb.WriteString("```\n")
	sb.WriteString(header + "\n")

	for row := 0; row < game.rows; row++ {
		for col := 0; col < game.cols; col++ {
			// Check if this cell is part of the win
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
					sb.WriteString(c4P1Win)
				} else {
					sb.WriteString(c4P2Win)
				}
			} else {
				switch game.board[row][col] {
				case 0:
					sb.WriteString(c4Empty)
				case 1:
					sb.WriteString(c4P1)
				case 2:
					sb.WriteString(c4P2)
				}
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString(header + "\n")
	sb.WriteString("```")

	var statusSB strings.Builder
	if game.gameOver {
		if statusMsg != "" {
			statusSB.WriteString(statusMsg)
		} else if game.winner == 0 {
			statusSB.WriteString(fmt.Sprintf(c4StatusDraw, game.player1ID, game.player2ID))
		} else {
			winnerID := game.player1ID
			loserID := game.player2ID
			if game.winner == 2 {
				winnerID = game.player2ID
				loserID = game.player1ID
			}
			statusSB.WriteString(fmt.Sprintf(c4StatusWin, loserID, winnerID))
		}
	} else {
		currentPlayerID := game.player1ID
		currentSymbol := c4P1
		if game.currentTurn == 2 {
			currentPlayerID = game.player2ID
			currentSymbol = c4P2
		}

		statusSB.WriteString(fmt.Sprintf(c4StatusTurn, currentPlayerID, currentSymbol))

		if statusMsg != "" {
			statusSB.WriteString("\n" + statusMsg)
		}

		if game.timerEnabled {
			expires := game.lastMoveTime.Add(game.timerDuration)
			statusSB.WriteString(fmt.Sprintf("\n⏱️ Expires <t:%d:R>", expires.Unix()))
		}
	}

	var containerComponents []discord.ContainerSubComponent

	containerComponents = append(containerComponents, discord.NewTextDisplay(sb.String()))

	if game.gameOver {
		containerComponents = append(containerComponents, discord.NewSeparator(discord.SeparatorSpacingSizeSmall).WithDivider(true))

		row := discord.NewActionRow(
			discord.NewButton(discord.ButtonStyleSecondary, "Play Again?", "connect4:disabled:info", "", 0).WithDisabled(true),
			discord.NewButton(discord.ButtonStyleSuccess, "Yes!", fmt.Sprintf("connect4:%s:yes", gameID), "", 0),
			discord.NewButton(discord.ButtonStyleDanger, "No.", fmt.Sprintf("connect4:%s:no", gameID), "", 0),
		)
		containerComponents = append(containerComponents, row)
	} else {
		containerComponents = append(containerComponents, discord.NewSeparator(discord.SeparatorSpacingSizeSmall).WithDivider(true))

		// Group buttons by max 5 per row
		for i := 0; i < game.cols; i += 5 {
			var rowButtons []discord.InteractiveComponent
			end := min(i+5, game.cols)
			for col := i + 1; col <= end; col++ {
				customID := fmt.Sprintf("connect4:%s:%d", gameID, col)
				btn := discord.NewButton(discord.ButtonStylePrimary, c4ColumnEmojis[col-1], customID, "", 0)
				if c4IsColumnFull(game, col-1) {
					btn = btn.WithDisabled(true)
				}
				rowButtons = append(rowButtons, btn)
			}
			containerComponents = append(containerComponents, discord.NewActionRow(rowButtons...))
		}

		forfeitBtn := discord.NewButton(discord.ButtonStyleDanger, c4Forfeit, fmt.Sprintf("connect4:%s:forfeit", gameID), "", 0)
		containerComponents = append(containerComponents, discord.NewActionRow(forfeitBtn))
	}

	return discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewTextDisplay(statusSB.String()),
			discord.NewContainer(containerComponents...),
		).
		Build()
}

// ===========================
// Game Logic
// ===========================

// c4MakeMove attempts to place a piece in the specified column
// Returns a status message if there's an error, empty string otherwise
func c4MakeMove(game *c4Game, col int) string {
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
		return c4StatusFull
	}

	game.board[row][col] = game.currentTurn
	game.lastMoveTime = time.Now()
	game.moveCount++

	if won, cells := c4CheckWin(game, row, col); won {
		game.gameOver = true
		game.winner = game.currentTurn
		game.winCells = cells
		return ""
	}

	if c4IsBoardFull(game) {
		game.gameOver = true
		game.winner = 0
		return ""
	}

	if game.currentTurn == 1 {
		game.currentTurn = 2
	} else {
		game.currentTurn = 1
	}

	return ""
}

// c4CheckWin checks if the last move at (row, col) resulted in a win
// Returns true and the winning cells if won, false otherwise
func c4CheckWin(game *c4Game, row, col int) (bool, [][2]int) {
	player := game.board[row][col]
	if player == 0 {
		return false, nil
	}

	// Helper to collect cells
	collect := func(dr, dc int) [][2]int {
		cells := [][2]int{{row, col}}
		// Forward
		for r, c := row+dr, col+dc; r >= 0 && r < game.rows && c >= 0 && c < game.cols && game.board[r][c] == player; r, c = r+dr, c+dc {
			cells = append(cells, [2]int{r, c})
		}
		// Backward
		for r, c := row-dr, col-dc; r >= 0 && r < game.rows && c >= 0 && c < game.cols && game.board[r][c] == player; r, c = r-dr, c-dc {
			cells = append(cells, [2]int{r, c})
		}
		return cells
	}

	// Check 4 directions
	dirs := [][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		cells := collect(d[0], d[1])
		if len(cells) >= c4ToWin {
			return true, cells
		}
	}

	return false, nil
}

// c4WouldWin checks if placing a piece in col would result in a win for player
func c4WouldWin(game *c4Game, col int, player int) bool {
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
	res, _ := c4CheckWin(game, row, col)
	game.board[row][col] = 0
	return res
}

// c4IsColumnFull checks if a column is full
func c4IsColumnFull(game *c4Game, col int) bool {
	return game.board[0][col] != 0
}

// c4IsBoardFull checks if the entire board is full
func c4IsBoardFull(game *c4Game) bool {
	for col := 0; col < game.cols; col++ {
		if !c4IsColumnFull(game, col) {
			return false
		}
	}
	return true
}

// ===========================
// AI Logic
// ===========================

// c4MakeAIMove executes an AI move based on the configured difficulty
func c4MakeAIMove(client *bot.Client, game *c4Game, gameID string) {
	activeGamesMu.Lock()
	defer activeGamesMu.Unlock()

	if game.gameOver || game.currentTurn != game.aiPlayerNum {
		return
	}

	var col int
	switch game.aiDifficulty {
	case c4Easy:
		col = c4AIRandomMove(game)
	case c4Medium:
		col = c4AIMediumMove(game)
	case c4Hard:
		col = c4AIHardMove(game)
	case c4Impossible:
		col = c4AIImpossibleMove(game)
	}

	statusMsg := c4MakeMove(game, col)

	builder := c4BuildMessage(game, gameID, statusMsg)
	if client != nil {
		_, _ = client.Rest.UpdateMessage(game.channelID, game.messageID, discord.NewMessageUpdateBuilder().
			SetComponents(builder.Components...).
			Build())

		// Start timer for human if enabled
		if !game.gameOver && game.timerEnabled {
			c4StartTimer(client, game, gameID, game.moveCount)
		}
	}
}

// c4AIRandomMove selects a random valid column (Easy difficulty)
func c4AIRandomMove(game *c4Game) int {
	var valid []int
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) {
			valid = append(valid, c)
		}
	}
	if len(valid) == 0 {
		return 0
	}
	return valid[rand.Intn(len(valid))]
}

// c4AIMediumMove tries to win, then block, then random (Medium difficulty)
func c4AIMediumMove(game *c4Game) int {
	ai := game.aiPlayerNum
	opponent := 3 - ai

	// 1. Win if possible
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, ai) {
			return c
		}
	}
	// 2. Block if must
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, opponent) {
			return c
		}
	}
	return c4AIRandomMove(game)
}

func c4AIHardMove(game *c4Game) int {
	ai := game.aiPlayerNum
	opponent := 3 - ai

	// 1. Win if possible
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, ai) {
			return c
		}
	}
	// 2. Block if must
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, opponent) {
			return c
		}
	}

	// 3. Strategic: Avoid moves that give the opponent a win on next turn
	safeCols := []int{}
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) {
			row := -1
			for r := game.rows - 1; r >= 0; r-- {
				if game.board[r][c] == 0 {
					row = r
					break
				}
			}

			// Simulate AI move
			game.board[row][c] = ai
			givesWin := false
			if row > 0 {
				if c4WouldWin(game, c, opponent) {
					givesWin = true
				}
			}
			game.board[row][c] = 0

			if !givesWin {
				safeCols = append(safeCols, c)
			}
		}
	}

	if len(safeCols) > 0 {
		centerCols := c4GetCenterOrder(game.cols)
		for _, c := range centerCols {
			for _, safe := range safeCols {
				if c == safe {
					return c
				}
			}
		}
	}

	return c4AIRandomMove(game)
}

func c4AIImpossibleMove(game *c4Game) int {
	ai := game.aiPlayerNum
	opponent := 3 - ai

	// Combination of aggressive blocking and center bias
	// 1. Win if possible
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, ai) {
			return c
		}
	}
	// 2. Block if must
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, opponent) {
			return c
		}
	}

	// 3. Heuristic: Prefer center columns and avoid traps
	centerCols := c4GetCenterOrder(game.cols)
	for _, c := range centerCols {
		if !c4IsColumnFull(game, c) {
			row := -1
			for r := game.rows - 1; r >= 0; r-- {
				if game.board[r][c] == 0 {
					row = r
					break
				}
			}
			game.board[row][c] = ai
			givesWin := false
			if row > 0 && c4WouldWin(game, c, opponent) {
				givesWin = true
			}
			game.board[row][c] = 0
			if !givesWin {
				return c
			}
		}
	}

	return c4AIRandomMove(game)
}

// ===========================
// UI Utilities
// ===========================

func c4GetHeader(cols int) string {
	var sb strings.Builder
	for i := range cols {
		sb.WriteString(c4ColumnEmojis[i])
	}
	return sb.String()
}

func c4GetCenterOrder(cols int) []int {
	center := cols / 2
	order := []int{center}
	left, right := center-1, center+1
	for left >= 0 || right < cols {
		if left >= 0 {
			order = append(order, left)
			left--
		}
		if right < cols {
			order = append(order, right)
			right++
		}
	}
	return order
}

func c4DisableInteractive(comp discord.LayoutComponent) discord.LayoutComponent {
	switch c := comp.(type) {
	case discord.ContainerComponent:
		for i, sub := range c.Components {
			c.Components[i] = c4DisableSub(sub)
		}
		return c
	case *discord.ContainerComponent:
		for i, sub := range c.Components {
			c.Components[i] = c4DisableSub(sub)
		}
		return c
	}
	return comp
}

func c4DisableSub(sub discord.ContainerSubComponent) discord.ContainerSubComponent {
	switch s := sub.(type) {
	case discord.ActionRowComponent:
		for i, inter := range s.Components {
			s.Components[i] = c4DisableInter(inter)
		}
		return s
	case *discord.ActionRowComponent:
		for i, inter := range s.Components {
			s.Components[i] = c4DisableInter(inter)
		}
		return s
	}
	return sub
}

func c4DisableInter(inter discord.InteractiveComponent) discord.InteractiveComponent {
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
