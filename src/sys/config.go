package sys

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Token        string
	GuildID      string
	DatabasePath string
}

func LoadConfig() (*Config, error) {
	err := godotenv.Load()
	if err != nil {
		return nil, fmt.Errorf(MsgConfigFailedToLoad, err)
	}

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		return nil, fmt.Errorf(MsgConfigMissingToken)
	}

	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "./data.db"
	}

	return &Config{
		Token:        token,
		GuildID:      os.Getenv("GUILD_ID"),
		DatabasePath: fmt.Sprintf("%s?_journal_mode=WAL&_timeout=5000", dbPath),
	}, nil
}
