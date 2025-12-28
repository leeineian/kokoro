package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/leeineian/minder/src/cmd"
	"github.com/leeineian/minder/src/proc"
	"github.com/leeineian/minder/src/sys"
)

func main() {
	// 1. Check for and kill old process
	if pidData, err := os.ReadFile(".bot.pid"); err == nil {
		if oldPid, err := strconv.Atoi(string(pidData)); err == nil && oldPid != os.Getpid() {
			if process, err := os.FindProcess(oldPid); err == nil {
				// Check if it's still running
				if err := process.Signal(syscall.Signal(0)); err == nil {
					sys.LogInfo("Killing running instance... (PID: %d)", oldPid)
					if err := process.Signal(syscall.SIGTERM); err == nil {
						// Wait for it to exit (up to 5 seconds)
						for i := 0; i < 50; i++ {
							if err := process.Signal(syscall.Signal(0)); err != nil {
								break // Process is gone
							}
							time.Sleep(100 * time.Millisecond)
						}
						sys.LogInfo("Old instance terminated.")
					} else {
						sys.LogWarn("Failed to kill old instance: %v", err)
					}
				}
			}
		}
	}

	// 2. Write PID file
	pid := os.Getpid()
	if err := os.WriteFile(".bot.pid", []byte(fmt.Sprintf("%d", pid)), 0644); err != nil {
		sys.LogWarn("Failed to write PID file: %v", err)
	}
	defer os.Remove(".bot.pid")

	// 3. Setup shutdown signal
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)

	// 4. Run bot (blocks until shutdown signal)
	if err := run(pid, sc); err != nil {
		sys.LogFatal("%v", err)
	}
}

func run(pid int, shutdownChan <-chan os.Signal) error {
	cfg, err := sys.LoadConfig()
	if err != nil {
		return fmt.Errorf("Failed to load config: %w", err)
	}

	// Initialize database
	if err := sys.InitDatabase(cfg.DatabasePath); err != nil {
		return fmt.Errorf("Failed to initialize database: %w", err)
	}
	defer sys.CloseDatabase()

	s, err := sys.CreateSession(cfg.Token)
	if err != nil {
		return fmt.Errorf("Failed to create Discord session: %w", err)
	}
	defer s.Close()

	// Register Slash Commands
	cmd.RegisterCatHandlers()
	cmd.RegisterDebugHandlers()
	cmd.RegisterReminderHandlers()
	cmd.RegisterUndertextHandlers()

	if err := sys.RegisterCommands(s, cfg.GuildID); err != nil {
		return fmt.Errorf("Failed to register commands: %w", err)
	}

	// Register and start daemons
	sys.RegisterDaemon(sys.LogReminder, func() { proc.StartReminderScheduler(s, sys.DB) })
	sys.RegisterDaemon(sys.LogRoleColorRotator, func() { proc.StartRoleColorRotator(s, sys.DB) })
	sys.RegisterDaemon(sys.LogStatusRotator, func() { proc.StartStatusRotator(s) })
	sys.RegisterDaemon(sys.LogLoopRotator, func() { proc.InitLoopRotator(s) })
	sys.StartDaemons()

	// Wait for shutdown signal
	sys.LogInfo("%s is online! (ID: %s) (PID: %d)", s.State.User.Username, s.State.User.ID, pid)
	<-shutdownChan
	fmt.Println()
	sys.LogInfo("Shutting down %s...", s.State.User.Username)

	return nil
}
