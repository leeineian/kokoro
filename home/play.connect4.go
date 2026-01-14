package home

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
	"github.com/leeineian/minder/sys"
)

// --- Constants & Types ---

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

type c4Difficulty int

const (
	c4Easy c4Difficulty = iota
	c4Medium
	c4Hard
	c4Impossible
)

type c4Game struct {
	board         [][]int
	rows          int
	cols          int
	player1ID     snowflake.ID
	player2ID     snowflake.ID
	isAI          bool
	aiDifficulty  c4Difficulty
	aiPlayerNum   int // 1 or 2
	currentTurn   int // 1 or 2
	gameOver      bool
	winner        int // 0=draw, 1=player1, 2=player2
	winCells      [][2]int
	moveCount     int
	timerEnabled  bool
	timerDuration time.Duration
	lastMoveTime  time.Time
	messageID     snowflake.ID
	channelID     snowflake.ID
	originalP1ID  snowflake.ID
	originalP2ID  snowflake.ID
}

// --- Global State & Initialization ---

var (
	activeGames   = make(map[string]*c4Game)
	activeGamesMu sync.RWMutex
)

func init() {
	sys.RegisterComponentHandler("connect4:", c4HandleMove)
}

// --- Interaction Handlers ---

// handlePlayConnect4 initiates a new game session (Slash Command)
func handlePlayConnect4(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	// Add panic recovery for debugging
	defer func() {
		if r := recover(); r != nil {
			sys.LogError("Panic in handlePlayConnect4: %v", r)
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
		if game.moveCount != moveCount || game.gameOver {
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

	activeGamesMu.RLock()
	game, exists := activeGames[gameID]
	activeGamesMu.RUnlock()

	if !exists {
		// Use ID check instead of nil for struct
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
		event.DeferUpdateMessage()
		return
	}

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

			// Remove buttons
			_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
				SetComponents().
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

// --- UI & Rendering ---

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
			end := i + 5
			if end > game.cols {
				end = game.cols
			}
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

// --- Game Logic ---

func c4MakeMove(game *c4Game, col int) string {
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

func c4IsColumnFull(game *c4Game, col int) bool {
	return game.board[0][col] != 0
}

func c4IsBoardFull(game *c4Game) bool {
	for col := 0; col < game.cols; col++ {
		if !c4IsColumnFull(game, col) {
			return false
		}
	}
	return true
}

// --- AI Logic ---

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
	}
}

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

func c4AIMediumMove(game *c4Game) int {
	// 1. Win if possible
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, 2) {
			return c
		}
	}
	// 2. Block if must
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, 1) {
			return c
		}
	}
	return c4AIRandomMove(game)
}

func c4AIHardMove(game *c4Game) int {
	// 1. Win if possible
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, 2) {
			return c
		}
	}
	// 2. Block if must
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, 1) {
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
			game.board[row][c] = 2
			givesWin := false
			if row > 0 {
				if c4WouldWin(game, c, 1) {
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
	// Combination of aggressive blocking and center bias
	// 1. Win if possible
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, 2) {
			return c
		}
	}
	// 2. Block if must
	for c := 0; c < game.cols; c++ {
		if !c4IsColumnFull(game, c) && c4WouldWin(game, c, 1) {
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
			game.board[row][c] = 2
			givesWin := false
			if row > 0 && c4WouldWin(game, c, 1) {
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

// --- UI Utilities ---

func c4GetHeader(cols int) string {
	var sb strings.Builder
	for i := 0; i < cols; i++ {
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
