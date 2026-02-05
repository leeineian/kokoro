package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/joho/godotenv"
	"github.com/mattn/go-sqlite3"
)

// --- Phase 1: Configuration & Environment ---

type Config struct {
	Token         string
	GuildID       string
	DatabasePath  string
	OwnerIDs      []string
	StreamingURL  string
	Silent        bool
	YoutubePrefix string
	YTMusicPrefix string
}

var GlobalConfig *Config

// LoadConfig initializes the configuration from environment variables.
func LoadConfig() (*Config, error) {
	_ = godotenv.Load()

	token := os.Getenv("DISCORD_TOKEN")
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		folder := "."
		if info, err := os.Stat("data"); err == nil && info.IsDir() {
			folder = "./data"
		}
		dbPath = filepath.Join(folder, GetProjectName()+".db")
	}

	silent, _ := strconv.ParseBool(os.Getenv("SILENT"))
	streamingURL := os.Getenv("STREAMING_URL")
	if streamingURL == "" {
		streamingURL = "https://www.twitch.tv/videos/1110069047"
	}

	ytPrefix := os.Getenv("VOICE_YT_PREFIX")
	if ytPrefix == "" {
		ytPrefix = "[YT]"
	}

	ytmPrefix := os.Getenv("VOICE_YTM_PREFIX")
	if ytmPrefix == "" {
		ytmPrefix = "[YTM]"
	}

	ownerIDsStr := os.Getenv("OWNER_IDS")
	var ownerIDs []string
	if ownerIDsStr != "" {
		ownerIDs = strings.Split(ownerIDsStr, ",")
		for i := range ownerIDs {
			ownerIDs[i] = strings.TrimSpace(ownerIDs[i])
		}
	}

	cfg := &Config{
		Token:         token,
		GuildID:       os.Getenv("GUILD_ID"),
		DatabasePath:  dbPath,
		OwnerIDs:      ownerIDs,
		StreamingURL:  streamingURL,
		Silent:        silent,
		YoutubePrefix: ytPrefix,
		YTMusicPrefix: ytmPrefix,
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if cfg.Silent {
		SetSilentMode(true)
	}

	GlobalConfig = cfg
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Token == "" {
		return fmt.Errorf(MsgConfigMissingToken)
	}
	if c.GuildID != "" && (len(c.GuildID) < 17 || len(c.GuildID) > 20) {
		return fmt.Errorf("invalid GUILD_ID: must be a valid Snowflake")
	}
	return nil
}

func GetProjectName() string {
	exePath, err := os.Executable()
	projectName := "bot"
	if err == nil {
		projectName = filepath.Base(exePath)
		projectName = strings.TrimSuffix(projectName, ".exe")

		if projectName == "main" || strings.HasPrefix(projectName, "go_build_") {
			if modData, err := os.ReadFile("go.mod"); err == nil {
				lines := strings.Split(string(modData), "\n")
				if len(lines) > 0 && strings.HasPrefix(lines[0], "module ") {
					parts := strings.Split(lines[0], "/")
					projectName = strings.TrimSpace(parts[len(parts)-1])
				}
			}
		}
	}
	return projectName
}

// --- Phase 2: Database Connection & Lifecycle ---

var DB *sql.DB

func InitDatabase(ctx context.Context, dataSourceName string) error {
	// Explicitly reference sqlite3 driver to avoid blank identifier
	// The driver registers itself via its init() function
	_ = sqlite3.SQLiteDriver{}

	var err error
	DB, err = sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return err
	}

	DB.SetMaxOpenConns(5)

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA cache_size=-2000;",
	}

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, p := range pragmas {
		if _, err := DB.ExecContext(initCtx, p); err != nil {
			return fmt.Errorf(MsgDatabasePragmaError, p, err)
		}
	}

	tx, err := DB.BeginTx(initCtx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tableQueries := []string{
		`CREATE TABLE IF NOT EXISTS reminders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			guild_id TEXT,
			message TEXT NOT NULL,
			remind_at DATETIME NOT NULL,
			send_to TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS guild_configs (
			guild_id TEXT PRIMARY KEY,
			random_color_role_id TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS bot_config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS loop_channels (
			channel_id TEXT PRIMARY KEY,
			channel_name TEXT NOT NULL,
			channel_type TEXT NOT NULL,
			rounds INTEGER DEFAULT 0,
			interval INTEGER DEFAULT 0,
			message TEXT DEFAULT '@everyone',
			webhook_author TEXT,
			webhook_avatar TEXT,
			use_thread INTEGER DEFAULT 0,
			thread_message TEXT,
			thread_count INTEGER DEFAULT 0,
			threads TEXT,
			is_running INTEGER DEFAULT 0,
			is_serial INTEGER DEFAULT 0,
			vote_panel TEXT,
			vote_role TEXT,
			vote_reaction TEXT,
			vote_message TEXT,
			vote_threshold INTEGER DEFAULT 0
		)`,
	}

	for _, q := range tableQueries {
		if _, err := tx.ExecContext(initCtx, q); err != nil {
			return fmt.Errorf(MsgDatabaseTableError, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	migrations := []string{
		"ALTER TABLE loop_channels ADD COLUMN thread_count INTEGER DEFAULT 0",
		"ALTER TABLE loop_channels ADD COLUMN vote_panel TEXT",
		"ALTER TABLE loop_channels ADD COLUMN vote_role TEXT",
		"ALTER TABLE loop_channels ADD COLUMN vote_reaction TEXT",
		"ALTER TABLE loop_channels ADD COLUMN vote_message TEXT",
		"ALTER TABLE loop_channels ADD COLUMN vote_threshold INTEGER DEFAULT 0",
		"ALTER TABLE loop_channels ADD COLUMN is_serial INTEGER DEFAULT 0",
	}

	for _, m := range migrations {
		if _, err := DB.ExecContext(initCtx, m); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf("failed to migrate database: %w", err)
			}
		}
	}

	LogDatabase(MsgDatabaseInitSuccess)
	return nil
}

func CloseDatabase() {
	if DB != nil {
		DB.Close()
	}
}

// --- Phase 3: Infrastructure & Bot Persistence ---

// BotConfig helpers are used by the loader for mode tracking and state.
func GetBotConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := DB.QueryRowContext(ctx, "SELECT value FROM bot_config WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func SetBotConfig(ctx context.Context, key, value string) error {
	_, err := DB.ExecContext(ctx, `
		INSERT INTO bot_config (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`, key, value)
	return err
}

// --- Phase 4: Application Logic (Reminders) ---

type Reminder struct {
	ID        int64
	UserID    snowflake.ID
	ChannelID snowflake.ID
	GuildID   snowflake.ID
	Message   string
	RemindAt  time.Time
	SendTo    string
	CreatedAt time.Time
}

func AddReminder(ctx context.Context, r *Reminder) error {
	_, err := DB.ExecContext(ctx, `
		INSERT INTO reminders (user_id, channel_id, guild_id, message, remind_at, send_to)
		VALUES (?, ?, ?, ?, ?, ?)
	`, r.UserID.String(), r.ChannelID.String(), r.GuildID.String(), r.Message, r.RemindAt, r.SendTo)
	return err
}

func GetRemindersForUser(ctx context.Context, userID snowflake.ID) ([]*Reminder, error) {
	rows, err := DB.QueryContext(ctx, `
		SELECT id, user_id, channel_id, guild_id, message, remind_at, send_to, created_at
		FROM reminders WHERE user_id = ? ORDER BY remind_at ASC
	`, userID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []*Reminder
	for rows.Next() {
		r := &Reminder{}
		var uid, cid, gid string
		err := rows.Scan(&r.ID, &uid, &cid, &gid, &r.Message, &r.RemindAt, &r.SendTo, &r.CreatedAt)
		if err != nil {
			return nil, err
		}
		r.UserID, err = snowflake.Parse(uid)
		if err != nil {
			return nil, fmt.Errorf("failed to parse user ID '%s' for reminder %d: %w", uid, r.ID, err)
		}
		r.ChannelID, err = snowflake.Parse(cid)
		if err != nil {
			return nil, fmt.Errorf("failed to parse channel ID '%s' for reminder %d: %w", cid, r.ID, err)
		}
		r.GuildID, err = snowflake.Parse(gid)
		if err != nil {
			// Guild ID can be empty for DMs, but if it's there it should be valid
			if gid != "" {
				return nil, fmt.Errorf("failed to parse guild ID '%s' for reminder %d: %w", gid, r.ID, err)
			}
		}
		reminders = append(reminders, r)
	}
	return reminders, nil
}

func ClaimDueReminders(ctx context.Context) ([]*Reminder, error) {
	rows, err := DB.QueryContext(ctx, `
		DELETE FROM reminders 
		WHERE remind_at <= ? 
		RETURNING id, user_id, channel_id, guild_id, message, remind_at, send_to, created_at
	`, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []*Reminder
	for rows.Next() {
		r := &Reminder{}
		var uid, cid, gid string
		err := rows.Scan(&r.ID, &uid, &cid, &gid, &r.Message, &r.RemindAt, &r.SendTo, &r.CreatedAt)
		if err != nil {
			return nil, err
		}
		r.UserID, err = snowflake.Parse(uid)
		if err != nil {
			return nil, fmt.Errorf("failed to parse user ID '%s' for claimed reminder %d: %w", uid, r.ID, err)
		}
		r.ChannelID, err = snowflake.Parse(cid)
		if err != nil {
			return nil, fmt.Errorf("failed to parse channel ID '%s' for claimed reminder %d: %w", cid, r.ID, err)
		}
		r.GuildID, err = snowflake.Parse(gid)
		if err != nil {
			if gid != "" {
				return nil, fmt.Errorf("failed to parse guild ID '%s' for claimed reminder %d: %w", gid, r.ID, err)
			}
		}
		reminders = append(reminders, r)
	}
	return reminders, nil
}

func GetDueReminders(ctx context.Context) ([]*Reminder, error) {
	rows, err := DB.QueryContext(ctx, `
		SELECT id, user_id, channel_id, guild_id, message, remind_at, send_to, created_at
		FROM reminders WHERE remind_at <= ? ORDER BY remind_at ASC
	`, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []*Reminder
	for rows.Next() {
		r := &Reminder{}
		var uid, cid, gid string
		err := rows.Scan(&r.ID, &uid, &cid, &gid, &r.Message, &r.RemindAt, &r.SendTo, &r.CreatedAt)
		if err != nil {
			return nil, err
		}
		r.UserID, err = snowflake.Parse(uid)
		if err != nil {
			return nil, fmt.Errorf("failed to parse user ID '%s' for due reminder %d: %w", uid, r.ID, err)
		}
		r.ChannelID, err = snowflake.Parse(cid)
		if err != nil {
			return nil, fmt.Errorf("failed to parse channel ID '%s' for due reminder %d: %w", cid, r.ID, err)
		}
		r.GuildID, err = snowflake.Parse(gid)
		if err != nil {
			if gid != "" {
				return nil, fmt.Errorf("failed to parse guild ID '%s' for due reminder %d: %w", gid, r.ID, err)
			}
		}
		reminders = append(reminders, r)
	}
	return reminders, nil
}

func DeleteReminder(ctx context.Context, id int64, userID snowflake.ID) (bool, error) {
	result, err := DB.ExecContext(ctx, "DELETE FROM reminders WHERE id = ? AND user_id = ?", id, userID.String())
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func DeleteAllRemindersForUser(ctx context.Context, userID snowflake.ID) (int64, error) {
	result, err := DB.ExecContext(ctx, "DELETE FROM reminders WHERE user_id = ?", userID.String())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func DeleteReminderByID(ctx context.Context, id int64) error {
	_, err := DB.ExecContext(ctx, "DELETE FROM reminders WHERE id = ?", id)
	return err
}

func GetRemindersCountForUser(ctx context.Context, userID snowflake.ID) (int, error) {
	var count int
	err := DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM reminders WHERE user_id = ?", userID.String()).Scan(&count)
	return count, err
}

func GetRemindersCount(ctx context.Context) (int, error) {
	var count int
	err := DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM reminders").Scan(&count)
	return count, err
}

// --- Phase 5: Application Logic (Loop Channels) ---

type LoopConfig struct {
	ChannelID     snowflake.ID
	ChannelName   string
	ChannelType   string
	Rounds        int
	Interval      int
	Message       string
	WebhookAuthor string
	WebhookAvatar string
	UseThread     bool
	ThreadMessage string
	ThreadCount   int
	Threads       string
	IsRunning     bool
	VoteChannelID string
	VoteRole      string
	VoteMessage   string
	VoteThreshold int
	IsSerial      bool
}

func AddLoopConfig(ctx context.Context, channelID snowflake.ID, config *LoopConfig) error {
	useThread := 0
	if config.UseThread {
		useThread = 1
	}

	_, err := DB.ExecContext(ctx, `
		INSERT INTO loop_channels (
			channel_id, channel_name, channel_type, rounds, interval,
			message, webhook_author, webhook_avatar,
			use_thread, thread_message, thread_count, threads,
			vote_panel, vote_role, vote_message, vote_threshold,
			is_running, is_serial
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT is_running FROM loop_channels WHERE channel_id = ?), 0), ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			channel_name = excluded.channel_name,
			channel_type = excluded.channel_type,
			rounds = excluded.rounds,
			interval = excluded.interval,
			message = excluded.message,
			webhook_author = excluded.webhook_author,
			webhook_avatar = excluded.webhook_avatar,
			use_thread = excluded.use_thread,
			thread_message = excluded.thread_message,
			thread_count = excluded.thread_count,
			threads = excluded.threads,
			vote_panel = excluded.vote_panel,
			vote_role = excluded.vote_role,
			vote_message = excluded.vote_message,
			vote_threshold = excluded.vote_threshold,
			is_serial = excluded.is_serial
	`, channelID.String(), config.ChannelName, config.ChannelType, config.Rounds, config.Interval,
		config.Message, config.WebhookAuthor, config.WebhookAvatar,
		useThread, config.ThreadMessage, config.ThreadCount, config.Threads,
		config.VoteChannelID, config.VoteRole, config.VoteMessage, config.VoteThreshold,
		channelID.String(), boolToInt(config.IsSerial))
	return err
}

func GetLoopConfig(ctx context.Context, channelID snowflake.ID) (*LoopConfig, error) {
	row := DB.QueryRowContext(ctx, `
		SELECT channel_id, channel_name, channel_type, rounds, interval,
			message, webhook_author, webhook_avatar,
			use_thread, thread_message, thread_count, threads, is_running,
			vote_panel, vote_role, vote_message, vote_threshold, is_serial
		FROM loop_channels WHERE channel_id = ?
	`, channelID.String())

	config := &LoopConfig{}
	var idStr string
	var message, author, avatar, threadMsg, threads sql.NullString
	var votePanel, voteRole, voteMessage sql.NullString
	var useThread, isRunning, isSerial int

	err := row.Scan(
		&idStr, &config.ChannelName, &config.ChannelType, &config.Rounds, &config.Interval,
		&message, &author, &avatar,
		&useThread, &threadMsg, &config.ThreadCount, &threads, &isRunning,
		&votePanel, &voteRole, &voteMessage, &config.VoteThreshold, &isSerial,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	config.ChannelID, err = snowflake.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse channel ID: %w", err)
	}
	config.Message = message.String
	if config.Message == "" {
		config.Message = "@everyone"
	}
	config.WebhookAuthor = author.String
	config.WebhookAvatar = avatar.String
	config.UseThread = useThread == 1
	config.ThreadMessage = threadMsg.String
	config.Threads = threads.String
	config.IsRunning = isRunning == 1
	config.VoteChannelID = votePanel.String
	config.VoteRole = voteRole.String
	config.VoteMessage = voteMessage.String
	config.IsSerial = isSerial == 1

	return config, nil
}

func GetAllLoopConfigs(ctx context.Context) ([]*LoopConfig, error) {
	rows, err := DB.QueryContext(ctx, `
		SELECT channel_id, channel_name, channel_type, rounds, interval,
			message, webhook_author, webhook_avatar,
			use_thread, thread_message, thread_count, threads, is_running,
			vote_panel, vote_role, vote_message, vote_threshold, is_serial
		FROM loop_channels
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []*LoopConfig
	for rows.Next() {
		config := &LoopConfig{}
		var idStr string
		var message, author, avatar, threadMsg, threads sql.NullString
		var votePanel, voteRole, voteMessage sql.NullString
		var useThread, isRunning, isSerial int

		err := rows.Scan(
			&idStr, &config.ChannelName, &config.ChannelType, &config.Rounds, &config.Interval,
			&message, &author, &avatar,
			&useThread, &threadMsg, &config.ThreadCount, &threads, &isRunning,
			&votePanel, &voteRole, &voteMessage, &config.VoteThreshold, &isSerial,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan loop config: %w", err)
		}

		config.ChannelID, err = snowflake.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse channel ID '%s' for loop config: %w", idStr, err)
		}
		config.Message = message.String
		if config.Message == "" {
			config.Message = "@everyone"
		}
		config.WebhookAuthor = author.String
		config.WebhookAvatar = avatar.String
		config.UseThread = useThread == 1
		config.ThreadMessage = threadMsg.String
		config.Threads = threads.String
		config.IsRunning = isRunning == 1
		config.VoteChannelID = votePanel.String
		config.VoteRole = voteRole.String
		config.VoteMessage = voteMessage.String
		config.IsSerial = isSerial == 1

		configs = append(configs, config)
	}

	return configs, nil
}

func DeleteLoopConfigDB(ctx context.Context, channelID snowflake.ID) error {
	_, err := DB.ExecContext(ctx, "DELETE FROM loop_channels WHERE channel_id = ?", channelID.String())
	return err
}

func SetLoopState(ctx context.Context, channelID snowflake.ID, running bool) error {
	val := 0
	if running {
		val = 1
	}
	_, err := DB.ExecContext(ctx, "UPDATE loop_channels SET is_running = ? WHERE channel_id = ?", val, channelID.String())
	return err
}

func ResetAllLoopStates(ctx context.Context) error {
	_, err := DB.ExecContext(ctx, "UPDATE loop_channels SET is_running = 0")
	return err
}

func UpdateLoopChannelName(ctx context.Context, channelID snowflake.ID, name string) error {
	_, err := DB.ExecContext(ctx, "UPDATE loop_channels SET channel_name = ? WHERE channel_id = ?", name, channelID.String())
	return err
}

// --- Phase 6: Application Logic (Guild Configs) ---

type GuildConfig struct {
	GuildID           string
	RandomColorRoleID string
	UpdatedAt         time.Time
}

func SetGuildRandomColorRole(ctx context.Context, guildID, roleID snowflake.ID) error {
	_, err := DB.ExecContext(ctx, `
		INSERT INTO guild_configs (guild_id, random_color_role_id) VALUES (?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET random_color_role_id = excluded.random_color_role_id, updated_at = CURRENT_TIMESTAMP
	`, guildID.String(), roleID.String())
	return err
}

func GetGuildRandomColorRole(ctx context.Context, guildID snowflake.ID) (snowflake.ID, error) {
	var roleIDStr sql.NullString
	err := DB.QueryRowContext(ctx, "SELECT random_color_role_id FROM guild_configs WHERE guild_id = ?", guildID.String()).Scan(&roleIDStr)
	if err == sql.ErrNoRows || !roleIDStr.Valid || roleIDStr.String == "" {
		return 0, nil
	}
	roleID, err := snowflake.Parse(roleIDStr.String)
	if err != nil {
		return 0, fmt.Errorf("failed to parse role ID: %w", err)
	}
	return roleID, err
}

func GetAllGuildRandomColorConfigs(ctx context.Context) (map[snowflake.ID]snowflake.ID, error) {
	rows, err := DB.QueryContext(ctx, "SELECT guild_id, random_color_role_id FROM guild_configs WHERE random_color_role_id IS NOT NULL AND random_color_role_id != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configs := make(map[snowflake.ID]snowflake.ID)
	for rows.Next() {
		var gStr, rStr string
		if err := rows.Scan(&gStr, &rStr); err != nil {
			return nil, fmt.Errorf("failed to scan guild config: %w", err)
		}
		gID, err := snowflake.Parse(gStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse guild ID '%s' in random colors: %w", gStr, err)
		}
		rID, err := snowflake.Parse(rStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse role ID '%s' in random colors: %w", rStr, err)
		}
		configs[gID] = rID
	}
	return configs, nil
}
