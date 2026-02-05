package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

func main() {
	// 0. Recover from panics (LogFatal uses panic to ensure defers run)
	defer func() {
		if r := recover(); r != nil {
			if msg, ok := r.(string); ok {
				fmt.Fprintf(os.Stderr, "\n[FATAL] %s\n", msg)
				os.Exit(1)
			}
			panic(r)
		}
	}()

	// 1. Load configuration early
	cfg, err := LoadConfig()
	if err != nil {
		LogError("Failed to load config: %v", err)
	}

	silent := flag.Bool("silent", false, "Disable all log output")
	skipReg := flag.Bool("skip-reg", false, "Skip command registration")
	clearAll := flag.Bool("clear-all", false, "Force clear guild commands (scan all guilds)")
	flag.Parse()

	// 2. Initialize Logger (handle flags)
	InitLogger(*silent, true)

	// 3. Initialize Database
	if err := InitDatabase(context.Background(), cfg.DatabasePath); err != nil {
		LogFatal("Failed to initialize database: %v", err)
	}
	defer CloseDatabase()

	// 4. Try to detect bot name
	botName := GetProjectName()
	var botID snowflake.ID
	if cfg != nil && cfg.Token != "" {
		if name, id, err := GetBotUsername(context.Background(), cfg.Token); err == nil {
			botName = name
			botID = id
		} else {
			LogError("Failed to get bot username: %v", err)
		}
	}

	LogInfo(MsgBotStarting, botName)

	// 4. Open or create the PID file
	f, err := os.OpenFile(".bot.pid", os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		LogFatal("Failed to open PID file: %v", err)
	}
	defer f.Close()

	// 5. Try to acquire an exclusive lock
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}

		if err != syscall.EWOULDBLOCK {
			LogFatal("Failed to lock PID file: %v", err)
		}

		var oldPid int
		_, _ = f.Seek(0, 0)
		if _, scanErr := fmt.Fscanf(f, "%d", &oldPid); scanErr != nil {
			_ = f.Close()
			<-ticker.C
			f, _ = os.OpenFile(".bot.pid", os.O_RDWR|os.O_CREATE, 0644)
			continue
		}

		if oldPid == os.Getpid() {
			break
		}

		process, procErr := os.FindProcess(oldPid)
		if procErr != nil {
			<-ticker.C
			continue
		}

		LogInfo(MsgBotKillingOld, oldPid)
		_ = process.Signal(syscall.SIGTERM)

		terminated := false
		timeout := time.After(5 * time.Second)
	waitLoop:
		for {
			select {
			case <-ticker.C:
				if err := process.Signal(syscall.Signal(0)); err != nil {
					terminated = true
					break waitLoop
				}
			case <-timeout:
				break waitLoop
			}
		}

		if !terminated {
			LogWarn("Old process %d is stubborn. Sending SIGKILL...", oldPid)
			_ = process.Signal(syscall.SIGKILL)

			killTimeout := time.After(2 * time.Second)
			killTicker := time.NewTicker(50 * time.Millisecond)
			defer killTicker.Stop()

		killWait:
			for {
				select {
				case <-killTicker.C:
					if err := process.Signal(syscall.Signal(0)); err != nil {
						break killWait
					}
				case <-killTimeout:
					LogWarn("Process %d still exists after SIGKILL", oldPid)
					break killWait
				}
			}
		}

		LogInfo(MsgBotOldTerminated)
	}

	// 6. We have the lock. Write our PID.
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d", os.Getpid())
	_ = f.Sync()

	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = os.Remove(".bot.pid")
	}()

	// 7. Run bot (blocks until shutdown signal)
	if err := run(cfg, *silent, *skipReg, *clearAll, botID); err != nil {
		LogFatal(MsgGenericError, err)
	}

	// 8. Handle Reboot
	if RestartRequested {
		LogInfo("Self-restarting process...")
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(".bot.pid")

		args := os.Args
		hasSkipReg := slices.Contains(args, "-skip-reg")
		if !hasSkipReg {
			args = append(args, "-skip-reg")
		}

		exePath, err := os.Executable()
		if err != nil {
			LogFatal("Failed to resolve executable path: %v", err)
		}

		err = syscall.Exec(exePath, args, os.Environ())
		if err != nil {
			LogFatal("Failed to re-execute: %v", err)
		}
	}
}

func run(cfg *Config, silent bool, skipReg bool, clearAll bool, botID snowflake.ID) error {
	// 1. Setup global context that responds to shutdown signals
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	defer stop()

	SetAppContext(ctx)

	// 2. Config is already loaded, but ensure it's valid
	if cfg == nil {
		var err error
		cfg, err = LoadConfig()
		if err != nil {
			return fmt.Errorf(MsgConfigFailedToLoad, err)
		}
	}

	// 3. Create disgo client
	client, err := CreateClient(ctx, cfg, botID)
	if err != nil {
		return fmt.Errorf("failed to create Discord client: %w", err)
	}
	defer client.Close(ctx)

	// 5. Command Registration
	if !skipReg {
		if err := RegisterCommands(client, cfg.GuildID, clearAll); err != nil {
			LogError(MsgBotRegisterFail, err)
		}
	} else {
		LogInfo("Skipping command registration as requested.")
	}

	// 6. Connect to Gateway
	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("failed to open gateway: %w", err)
	}

	<-ctx.Done()
	if !silent {
		fmt.Println()
	}

	// Graceful Shutdown
	LogInfo("Shutting down all daemons...")
	ShutdownDaemons(context.Background())

	// Dynamic shutdown logging
	if botUser, ok := client.Caches.SelfUser(); ok {
		LogInfo(MsgBotShutdown, botUser.Username)
	} else {
		LogInfo(MsgBotShutdown, GetProjectName())
	}

	return nil
}
