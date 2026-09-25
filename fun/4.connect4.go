package fun

import (
	"fmt"
	"math/rand"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

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

func handlePlayConnect4(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	defer func() {
		if r := recover(); r != nil {
			hooks.LogError("Panic in handlePlayConnect4: %v", r)
			fmt.Printf("%s\n", debug.Stack())
		}
	}()

	var opponentID *snowflake.ID
	difficulty := connect4Normal

	var isOpponentBot bool
	if opponent, ok := data.OptUser(OptOpponent); ok {
		opponentID = &opponent.ID
		isOpponentBot = opponent.Bot
	}

	if diff, ok := data.OptString(OptDifficulty); ok {
		switch diff {
		case ChoiceEasy:
			difficulty = connect4Easy
		case ChoiceNormal:
			difficulty = connect4Normal
		case ChoiceHard:
			difficulty = connect4Hard
		}
	}

	timerSeconds := 0
	if timer, ok := data.OptInt(OptTimer); ok {
		timerSeconds = timer
	}

	rows, cols := 6, 7
	if size, ok := data.OptString(OptSize); ok {
		switch size {
		case ChoiceSmall:
			rows, cols = 4, 5
		case ChoiceClassic:
			rows, cols = 6, 7
		case ChoiceLarge:
			rows, cols = 8, 9
		case ChoiceMaster:
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
		event.CreateMessage(discord.NewMessageCreate().WithContent(fmt.Sprintf(MsgGameAlreadyActive, gid)).WithEphemeral(true))
		return
	}
	if opponentID != nil && *opponentID != appID {
		if gid, ok := userActiveGame[*opponentID]; ok {
			userActiveGameMu.Unlock()
			event.CreateMessage(discord.NewMessageCreate().WithContent(fmt.Sprintf(MsgGameOpponentActive, *opponentID, gid)).WithEphemeral(true))
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
	if !game.isAI && rand.Intn(2) == 1 {
		variant = VariantInverted
	}
	game.colorVariant = variant

	activeConnect4GamesMu.Lock()
	activeConnect4Games[gameID] = game
	activeConnect4GamesMu.Unlock()

	builder := connect4BuildMessage(game, gameID, "")
	if err := hooks.RespondInteractionContainerV2(*event.Client(), event, hooks.NewV2Container(builder...), false); err != nil {
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
		connect4StartTimer(*event.Client(), game, gameID, 0)
	}

	if game.isAI && game.aiPlayerNum == 1 {
		time.AfterFunc(1*time.Second, func() {
			connect4MakeAIMove(*event.Client(), game, gameID)
		})
	}
}

// connect4StartTimer watches for turn expiry and forfeits if time runs out
func connect4StartTimer(client bot.Client, game *connect4Game, gameID string, moveCount int) {
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
		_, _ = hooks.EditContainerV2(client, game.channelID, game.messageID, builder, nil, nil)

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

		var newComponents []any
		for i, comp := range event.Message.Components {
			modified := connect4DisableInteractive(comp)
			if i == 0 {
				if td, ok := modified.(discord.TextDisplayComponent); ok {
					newComponents = append(newComponents, hooks.NewTextDisplay(td.Content+"\n"+connect4StatusInactive))
					continue
				} else if td, ok := modified.(*discord.TextDisplayComponent); ok {
					newComponents = append(newComponents, hooks.NewTextDisplay(td.Content+"\n"+connect4StatusInactive))
					continue
				}
			}
			newComponents = append(newComponents, modified)
		}

		_ = hooks.UpdateInteractionContainerV2(*event.Client(), event, hooks.NewV2Container(newComponents...))
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
			event.CreateMessage(discord.NewMessageCreate().
				WithContent(MsgGameNotPlayer).
				WithEphemeral(true))
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

			var newComponents []any

			if len(fullMsg) > 3 {
				newComponents = append(newComponents, fullMsg[:3]...)
			} else {
				newComponents = append(newComponents, fullMsg...)
			}

			_ = hooks.UpdateInteractionContainerV2(*event.Client(), event, hooks.NewV2Container(newComponents...))
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

		builder := connect4BuildMessage(game, gameID, MsgGameRestarted)
		_ = hooks.UpdateInteractionContainerV2(*event.Client(), event, hooks.NewV2Container(builder...))

		if game.timerEnabled {
			connect4StartTimer(*event.Client(), game, gameID, 0)
		}
		if game.isAI && game.aiPlayerNum == 1 {
			time.AfterFunc(1*time.Second, func() {
				connect4MakeAIMove(*event.Client(), game, gameID)
			})
		}
		return
	}

	if action == "forfeit" {
		userID := event.User().ID
		if userID != game.player1ID && userID != game.player2ID {
			event.CreateMessage(discord.NewMessageCreate().
				WithContent(MsgGameNotPlayer).
				WithEphemeral(true))
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
		hooks.UpdateInteractionContainerV2(*event.Client(), event, hooks.NewV2Container(builder...))
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
		event.CreateMessage(discord.NewMessageCreate().
			WithContent(MsgGameNotTurn).
			WithEphemeral(true))
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
	hooks.UpdateInteractionContainerV2(*event.Client(), event, hooks.NewV2Container(builder...))

	if !game.gameOver {
		if game.timerEnabled {
			connect4StartTimer(*event.Client(), game, gameID, game.moveCount)
		}
		if game.isAI && game.currentTurn == game.aiPlayerNum {
			time.AfterFunc(1*time.Second, func() {
				connect4MakeAIMove(*event.Client(), game, gameID)
			})
		}
	}
}

// ===========================
// UI & Rendering
// ===========================

func connect4BuildMessage(game *connect4Game, gameID string, statusMsg string) []any {
	var sb strings.Builder
	header := connect4GetHeader(game.cols)
	sb.WriteString(header)
	sb.WriteString("\n")

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

	sb.WriteString(header)
	sb.WriteString("\n")

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
			statusSB.WriteString("\n")
			statusSB.WriteString(statusMsg)
		}

		if game.timerEnabled {
			expires := game.lastMoveTime.Add(game.timerDuration)
			statusSB.WriteString(fmt.Sprintf("\n⏱️ Expires <t:%d:R>", expires.Unix()))
		}
	}

	var components []any

	components = append(components, hooks.NewTextDisplay(statusSB.String()))
	components = append(components, hooks.NewTextDisplay(sb.String()))

	if game.gameOver {
		components = append(components, hooks.NewSeparator(true))

		row := discord.NewActionRow(
			discord.NewButton(discord.ButtonStyleSecondary, LabelPlayAgain, CIDConnect4Disabled, "", 0).WithDisabled(true),
			discord.NewButton(discord.ButtonStyleSuccess, LabelYes, fmt.Sprintf(CIDConnect4Yes, gameID), "", 0),
			discord.NewButton(discord.ButtonStyleDanger, LabelNo, fmt.Sprintf(CIDConnect4No, gameID), "", 0),
		)
		components = append(components, row)
	} else {
		components = append(components, hooks.NewSeparator(true))

		for i := 0; i < game.cols; i += 5 {
			var rowButtons []discord.InteractiveComponent
			end := min(i+5, game.cols)
			for col := i + 1; col <= end; col++ {
				customID := fmt.Sprintf(CIDConnect4Move, gameID, col)
				btn := discord.NewButton(discord.ButtonStylePrimary, connect4ColumnEmojis[col-1], customID, "", 0)
				if connect4IsColumnFull(game, col-1) {
					btn = btn.WithDisabled(true)
				}
				rowButtons = append(rowButtons, btn)
			}
			components = append(components, discord.NewActionRow(rowButtons...))
		}

		forfeitBtn := discord.NewButton(discord.ButtonStyleDanger, LabelForfeit, fmt.Sprintf(CIDConnect4Forfeit, gameID), "", 0)
		components = append(components, discord.NewActionRow(forfeitBtn))
	}

	return components
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
func connect4MakeAIMove(client bot.Client, game *connect4Game, gameID string) {
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
	_, _ = hooks.EditContainerV2(client, game.channelID, game.messageID, builder, nil, nil)

	if !game.gameOver && game.timerEnabled {
		connect4StartTimer(client, game, gameID, game.moveCount)
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
