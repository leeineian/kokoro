package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/leeineian/minder/home"
	_ "github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

func main() {
	// Parse flags
	silent := flag.Bool("silent", false, "Disable all log output")
	flag.Parse()

	if *silent {
		sys.SetSilentMode(true)
	}

	// 1. Open or create the PID file
	f, err := os.OpenFile(".bot.pid", os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		sys.LogFatal("Failed to open PID file: %v", err)
	}
	defer f.Close()

	// 2. Try to acquire an exclusive lock
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break // Lock acquired!
		}

		if err != syscall.EWOULDBLOCK {
			sys.LogFatal("Failed to lock PID file: %v", err)
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

		sys.LogInfo(sys.MsgBotKillingOld, oldPid)
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
			sys.LogWarn("Old process %d is stubborn. Sending SIGKILL...", oldPid)
			_ = process.Signal(syscall.SIGKILL)
			time.Sleep(200 * time.Millisecond) // Give OS time to clean up
		}

		sys.LogInfo(sys.MsgBotOldTerminated)
		// Loop will retry Flock on next iteration
	}

	// 3. We have the lock. Write our PID.
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d", os.Getpid())
	_ = f.Sync()

	// Ensure the lock is held for the duration and the file is cleaned up on exit
	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = os.Remove(".bot.pid")
	}()

	// 4. Run bot (blocks until shutdown signal)
	if err := run(*silent); err != nil {
		sys.LogFatal(sys.MsgGenericError, err)
	}
}

func run(silent bool) error {
	// 1. Setup global context that responds to shutdown signals
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	defer stop()

	sys.SetAppContext(ctx)

	// 2. Load configuration
	cfg, err := sys.LoadConfig()
	if err != nil {
		return fmt.Errorf(sys.MsgConfigFailedToLoad, err)
	}

	// 3. Create disgo client
	client, err := sys.CreateClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create Discord client: %w", err)
	}
	defer client.Close(ctx)

	// 4. Initialize database
	if err := sys.InitDatabase(ctx, cfg.DatabasePath); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer sys.CloseDatabase()

	// 5. Command Registration
	if err := sys.RegisterCommands(client, cfg.GuildID); err != nil {
		sys.LogError(sys.MsgBotRegisterFail, err)
	}

	// 6. Connect to Gateway
	sys.LogInfo(sys.MsgBotStarting, sys.GetProjectName())
	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("failed to open gateway: %w", err)
	}

	<-ctx.Done()
	if !silent {
		fmt.Println()
	}

	// Dynamic shutdown logging
	if botUser, ok := client.Caches.SelfUser(); ok {
		sys.LogInfo(sys.MsgBotShutdown, botUser.Username)
	} else {
		sys.LogInfo(sys.MsgBotShutdown, sys.GetProjectName())
	}

	return nil
}
