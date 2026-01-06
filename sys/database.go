package sys

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/disgoorg/snowflake/v2"
	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func InitDatabase(ctx context.Context, dataSourceName string) error {
	var err error
	DB, err = sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return err
	}

	// Set connection pool settings for better concurrency
	// SQLite works best with a single connection for writing, but WAL allows multiple readers.
	DB.SetMaxOpenConns(1)

	// Apply optimizations
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA cache_size=-2000;", // ~2MB cache
	}
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, p := range pragmas {
		if _, err := DB.ExecContext(initCtx, p); err != nil {
			return fmt.Errorf(MsgDatabasePragmaError, p, err)
		}
	}

	// Create tables in a single transaction for speed and atomicity
	tx, err := DB.BeginTx(initCtx, nil)
	if err != nil {
		return err
	}

	// Ensure transaction rollback on error
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
			threads TEXT,
			is_running INTEGER DEFAULT 0
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

	LogDatabase(MsgDatabaseInitSuccess)
	return nil
}

// --- Reminder Logic ---

type Reminder struct {
	ID        int64
	UserID    snowflake.ID
	ChannelID snowflake.ID
	GuildID   snowflake.ID
	Message   string
	RemindAt  time.Time
	SendTo    string // "dm" or "channel"
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
		r.UserID, _ = snowflake.Parse(uid)
		r.ChannelID, _ = snowflake.Parse(cid)
		r.GuildID, _ = snowflake.Parse(gid)
		reminders = append(reminders, r)
	}
	return reminders, nil
}

func ClaimDueReminders(ctx context.Context) ([]*Reminder, error) {
	// Using DELETE ... RETURNING is atomic in SQLite 3.35.0+
	// This ensures each reminder is "claimed" by exactly one caller.
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
		r.UserID, _ = snowflake.Parse(uid)
		r.ChannelID, _ = snowflake.Parse(cid)
		r.GuildID, _ = snowflake.Parse(gid)
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
		r.UserID, _ = snowflake.Parse(uid)
		r.ChannelID, _ = snowflake.Parse(cid)
		r.GuildID, _ = snowflake.Parse(gid)
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

// --- Guild Config Logic ---

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
	roleID, _ := snowflake.Parse(roleIDStr.String)
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
			continue
		}
		gID, _ := snowflake.Parse(gStr)
		rID, _ := snowflake.Parse(rStr)
		configs[gID] = rID
	}
	return configs, nil
}

// --- Loop Logic ---

type LoopConfig struct {
	ChannelID     snowflake.ID
	ChannelName   string
	ChannelType   string // "category" (loop system is category-only)
	Rounds        int
	Interval      int // milliseconds
	Message       string
	WebhookAuthor string
	WebhookAvatar string
	UseThread     bool
	ThreadMessage string
	Threads       string // JSON
	IsRunning     bool
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
			use_thread, thread_message, threads, is_running
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT is_running FROM loop_channels WHERE channel_id = ?), 0))
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
			threads = excluded.threads
	`, channelID.String(), config.ChannelName, config.ChannelType, config.Rounds, config.Interval,
		config.Message, config.WebhookAuthor, config.WebhookAvatar,
		useThread, config.ThreadMessage, config.Threads, channelID.String())
	return err
}

func GetLoopConfig(ctx context.Context, channelID snowflake.ID) (*LoopConfig, error) {
	row := DB.QueryRowContext(ctx, `
		SELECT channel_id, channel_name, channel_type, rounds, interval,
			message, webhook_author, webhook_avatar,
			use_thread, thread_message, threads, is_running
		FROM loop_channels WHERE channel_id = ?
	`, channelID.String())

	config := &LoopConfig{}
	var idStr string
	var message, author, avatar, threadMsg, threads sql.NullString
	var useThread, isRunning int

	err := row.Scan(
		&idStr, &config.ChannelName, &config.ChannelType, &config.Rounds, &config.Interval,
		&message, &author, &avatar,
		&useThread, &threadMsg, &threads, &isRunning,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	config.ChannelID, _ = snowflake.Parse(idStr)
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

	return config, nil
}

func GetAllLoopConfigs(ctx context.Context) ([]*LoopConfig, error) {
	rows, err := DB.QueryContext(ctx, `
		SELECT channel_id, channel_name, channel_type, rounds, interval,
			message, webhook_author, webhook_avatar,
			use_thread, thread_message, threads, is_running
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
		var useThread, isRunning int

		err := rows.Scan(
			&idStr, &config.ChannelName, &config.ChannelType, &config.Rounds, &config.Interval,
			&message, &author, &avatar,
			&useThread, &threadMsg, &threads, &isRunning,
		)
		if err != nil {
			continue
		}

		config.ChannelID, _ = snowflake.Parse(idStr)
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

		configs = append(configs, config)
	}

	return configs, nil
}

func DeleteLoopConfig(ctx context.Context, channelID snowflake.ID) error {
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

func UpdateLoopChannelName(ctx context.Context, channelID snowflake.ID, name string) error {
	_, err := DB.ExecContext(ctx, "UPDATE loop_channels SET channel_name = ? WHERE channel_id = ?", name, channelID.String())
	return err
}

// --- General Config & Stats ---

func GetRemindersCount(ctx context.Context) (int, error) {
	var count int
	err := DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM reminders").Scan(&count)
	return count, err
}

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

func CloseDatabase() {
	if DB != nil {
		DB.Close()
	}
}
