package sys

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func InitDatabase(dataSourceName string) error {
	var err error
	DB, err = sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return err
	}

	// Set connection pool settings for better concurrency
	// WAL mode allows multiple readers and one writer.
	// With _timeout=5000, we can safely allow multiple connections.
	DB.SetMaxOpenConns(5)
	DB.SetMaxIdleConns(5)

	// Create reminders table
	_, err = DB.Exec(`
		CREATE TABLE IF NOT EXISTS reminders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			guild_id TEXT,
			message TEXT NOT NULL,
			remind_at DATETIME NOT NULL,
			send_to TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Create Guild Configs table
	_, err = DB.Exec(`
		CREATE TABLE IF NOT EXISTS guild_configs (
			guild_id TEXT PRIMARY KEY,
			random_color_role_id TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Create Bot Config table
	_, err = DB.Exec(`
		CREATE TABLE IF NOT EXISTS bot_config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Create Loop Channels table
	_, err = DB.Exec(`
		CREATE TABLE IF NOT EXISTS loop_channels (
			channel_id TEXT PRIMARY KEY,
			channel_name TEXT NOT NULL,
			channel_type TEXT NOT NULL,
			rounds INTEGER DEFAULT 0,
			interval INTEGER DEFAULT 0,
			active_channel_name TEXT,
			inactive_channel_name TEXT,
			message TEXT DEFAULT '@everyone',
			webhook_author TEXT,
			webhook_avatar TEXT,
			use_thread INTEGER DEFAULT 0,
			thread_message TEXT,
			threads TEXT,
			is_running INTEGER DEFAULT 0
		)
	`)
	if err != nil {
		return err
	}

	LogDatabase("Database initialized successfully")
	return nil
}

// LoopConfig represents a loop channel configuration
type LoopConfig struct {
	ChannelID           string
	ChannelName         string
	ChannelType         string // "category" or "channel"
	Rounds              int
	Interval            int // milliseconds
	ActiveChannelName   string
	InactiveChannelName string
	Message             string
	WebhookAuthor       string
	WebhookAvatar       string
	UseThread           bool
	ThreadMessage       string
	Threads             string // JSON
	IsRunning           bool
}

// AddLoopConfig adds or updates a loop channel configuration
func AddLoopConfig(channelID string, config *LoopConfig) error {
	useThread := 0
	if config.UseThread {
		useThread = 1
	}

	_, err := DB.Exec(`
		INSERT INTO loop_channels (
			channel_id, channel_name, channel_type, rounds, interval,
			active_channel_name, inactive_channel_name, message, webhook_author, webhook_avatar,
			use_thread, thread_message, threads, is_running
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT is_running FROM loop_channels WHERE channel_id = ?), 0))
		ON CONFLICT(channel_id) DO UPDATE SET
			channel_name = excluded.channel_name,
			channel_type = excluded.channel_type,
			rounds = excluded.rounds,
			interval = excluded.interval,
			active_channel_name = excluded.active_channel_name,
			inactive_channel_name = excluded.inactive_channel_name,
			message = excluded.message,
			webhook_author = excluded.webhook_author,
			webhook_avatar = excluded.webhook_avatar,
			use_thread = excluded.use_thread,
			thread_message = excluded.thread_message,
			threads = excluded.threads
	`, channelID, config.ChannelName, config.ChannelType, config.Rounds, config.Interval,
		config.ActiveChannelName, config.InactiveChannelName, config.Message, config.WebhookAuthor, config.WebhookAvatar,
		useThread, config.ThreadMessage, config.Threads, channelID)
	return err
}

// GetLoopConfig retrieves a loop channel configuration
func GetLoopConfig(channelID string) (*LoopConfig, error) {
	row := DB.QueryRow(`
		SELECT channel_id, channel_name, channel_type, rounds, interval,
			active_channel_name, inactive_channel_name, message, webhook_author, webhook_avatar,
			use_thread, thread_message, threads, is_running
		FROM loop_channels WHERE channel_id = ?
	`, channelID)

	config := &LoopConfig{}
	var activeName, inactiveName, message, author, avatar, threadMsg, threads sql.NullString
	var useThread, isRunning int

	err := row.Scan(
		&config.ChannelID, &config.ChannelName, &config.ChannelType, &config.Rounds, &config.Interval,
		&activeName, &inactiveName, &message, &author, &avatar,
		&useThread, &threadMsg, &threads, &isRunning,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	config.ActiveChannelName = activeName.String
	config.InactiveChannelName = inactiveName.String
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

// GetAllLoopConfigs retrieves all loop channel configurations
func GetAllLoopConfigs() ([]*LoopConfig, error) {
	rows, err := DB.Query(`
		SELECT channel_id, channel_name, channel_type, rounds, interval,
			active_channel_name, inactive_channel_name, message, webhook_author, webhook_avatar,
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
		var activeName, inactiveName, message, author, avatar, threadMsg, threads sql.NullString
		var useThread, isRunning int

		err := rows.Scan(
			&config.ChannelID, &config.ChannelName, &config.ChannelType, &config.Rounds, &config.Interval,
			&activeName, &inactiveName, &message, &author, &avatar,
			&useThread, &threadMsg, &threads, &isRunning,
		)
		if err != nil {
			continue
		}

		config.ActiveChannelName = activeName.String
		config.InactiveChannelName = inactiveName.String
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

// DeleteLoopConfig deletes a loop channel configuration
func DeleteLoopConfig(channelID string) error {
	_, err := DB.Exec("DELETE FROM loop_channels WHERE channel_id = ?", channelID)
	return err
}

// SetLoopState sets the running state of a loop
func SetLoopState(channelID string, running bool) error {
	val := 0
	if running {
		val = 1
	}
	_, err := DB.Exec("UPDATE loop_channels SET is_running = ? WHERE channel_id = ?", val, channelID)
	return err
}

// UpdateLoopChannelName updates the stored channel name
func UpdateLoopChannelName(channelID, name string) error {
	_, err := DB.Exec("UPDATE loop_channels SET channel_name = ? WHERE channel_id = ?", name, channelID)
	return err
}

func GetRemindersCount() (int, error) {
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM reminders").Scan(&count)
	return count, err
}

func GetBotConfig(key string) (string, error) {
	var value string
	err := DB.QueryRow("SELECT value FROM bot_config WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func SetBotConfig(key, value string) error {
	_, err := DB.Exec(`
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
