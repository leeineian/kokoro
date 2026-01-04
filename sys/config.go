package sys

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Token        string
	GuildID      string
	DatabasePath string
	OwnerIDs     []string
	StreamingURL string
	Silent       bool
}

var GlobalConfig *Config

// Validate ensures the configuration is valid and meets requirements.
func (c *Config) Validate() error {
	if c.Token == "" {
		return fmt.Errorf(MsgConfigMissingToken)
	}

	// Basic Snowflake validation for GuildID if provided
	if c.GuildID != "" && (len(c.GuildID) < 17 || len(c.GuildID) > 20) {
		return fmt.Errorf("invalid GUILD_ID: must be a valid Snowflake")
	}

	return nil
}

func LoadConfig() (*Config, error) {
	_ = godotenv.Load() // Ignore error if .env doesn't exist

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
		streamingURL = "https://www.twitch.tv/videos/1110069047" // Fallback
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
		Token:        token,
		GuildID:      os.Getenv("GUILD_ID"),
		DatabasePath: fmt.Sprintf("%s?_journal_mode=WAL&_timeout=5000", dbPath),
		OwnerIDs:     ownerIDs,
		StreamingURL: streamingURL,
		Silent:       silent,
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Integration with logger (#4)
	if cfg.Silent {
		SetSilentMode(true)
	}

	GlobalConfig = cfg
	return cfg, nil
}

// GetProjectName returns the name of the project dynamically
func GetProjectName() string {
	exePath, err := os.Executable()
	projectName := "bot"
	if err == nil {
		projectName = filepath.Base(exePath)
		projectName = strings.TrimSuffix(projectName, ".exe")

		// If running via 'go run' or unnamed binary, try to get name from go.mod
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
