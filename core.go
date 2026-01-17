package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// 0. Recover from panics (LogFatal uses panic to ensure defers run)
	defer func() {
		if r := recover(); r != nil {
			// If it's a string, it's likely from LogFatal
			if msg, ok := r.(string); ok {
				fmt.Fprintf(os.Stderr, "\n[FATAL] %s\n", msg)
				os.Exit(1)
			}
			// Otherwise re-panic
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

	// 3. Try to detect bot name
	botName := GetProjectName()
	if cfg != nil && cfg.Token != "" {
		if name, err := GetBotUsername(cfg.Token); err == nil {
			botName = name
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
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break // Lock acquired!
		}

		if err != syscall.EWOULDBLOCK {
			LogFatal("Failed to lock PID file: %v", err)
		}

		// Lock is held by another process. Read the PID and kill it.
		var oldPid int
		_, _ = f.Seek(0, 0)
		if _, scanErr := fmt.Fscanf(f, "%d", &oldPid); scanErr != nil {
			// File is empty or corrupt but locked? Wait a moment and retry.
			time.Sleep(100 * time.Millisecond)
			_ = f.Close()
			f, _ = os.OpenFile(".bot.pid", os.O_RDWR|os.O_CREATE, 0644)
			continue
		}

		if oldPid == os.Getpid() {
			break // Should not happen with LOCK_EX, but safety first
		}

		process, procErr := os.FindProcess(oldPid)
		if procErr != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		LogInfo(MsgBotKillingOld, oldPid)
		_ = process.Signal(syscall.SIGTERM)

		// Wait up to 5 seconds for it to exit
		terminated := false
		for i := 0; i < 50; i++ {
			if err := process.Signal(syscall.Signal(0)); err != nil {
				terminated = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if !terminated {
			LogWarn("Old process %d is stubborn. Sending SIGKILL...", oldPid)
			_ = process.Signal(syscall.SIGKILL)
			time.Sleep(200 * time.Millisecond) // Give OS time to clean up
		}

		LogInfo(MsgBotOldTerminated)
		// Loop will retry Flock on next iteration
	}

	// 6. We have the lock. Write our PID.
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d", os.Getpid())
	_ = f.Sync()

	// Ensure the lock is held for the duration and the file is cleaned up on exit
	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = os.Remove(".bot.pid")
	}()

	// 7. Run bot (blocks until shutdown signal)
	if err := run(cfg, *silent, *skipReg, *clearAll); err != nil {
		LogFatal(MsgGenericError, err)
	}

	// 8. Handle Reboot
	if RestartRequested {
		LogInfo("Self-restarting process...")
		// Manually trigger cleanup since syscall.Exec won't run defers
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(".bot.pid")

		// Re-execute the binary with the same arguments and environment
		// Add -skip-reg to avoid hitting rate limits on every reboot
		args := os.Args
		hasSkipReg := false
		for _, arg := range args {
			if arg == "-skip-reg" {
				hasSkipReg = true
				break
			}
		}
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

func run(cfg *Config, silent bool, skipReg bool, clearAll bool) error {
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
	client, err := CreateClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create Discord client: %w", err)
	}
	defer client.Close(ctx)

	// 4. Initialize database
	if err := InitDatabase(ctx, cfg.DatabasePath); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer CloseDatabase()

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
