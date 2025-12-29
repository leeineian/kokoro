package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/leeineian/minder/src/cmd"
	_ "github.com/leeineian/minder/src/proc"
	"github.com/leeineian/minder/src/sys"
)

func main() {
	// Parse flags
	silent := flag.Bool("silent", false, "Disable all log output")
	flag.Parse()

	if *silent {
		sys.SetSilentMode(true)
	}

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
	if err := run(pid, sc, *silent); err != nil {
		sys.LogFatal("%v", err)
	}
}

func run(pid int, shutdownChan <-chan os.Signal, silent bool) error {
	// Load configuration
	cfg, err := sys.LoadConfig()
	if err != nil {
		return fmt.Errorf("Failed to load config: %w", err)
	}

	// Initialize database
	if err := sys.InitDatabase(cfg.DatabasePath); err != nil {
		return fmt.Errorf("Failed to initialize database: %w", err)
	}
	defer sys.CloseDatabase()

	// Create Discord session
	s, err := sys.CreateSession(cfg.Token)
	if err != nil {
		return fmt.Errorf("Failed to create Discord session: %w", err)
	}
	defer s.Close()

	// 1. Background Command Registration (Parallel)
	go func() {
		if err := sys.RegisterCommands(s, cfg.GuildID); err != nil {
			sys.LogError("Background command registration failed: %v", err)
		}
	}()

	// 2. Background Daemons (Automated via init() functions in /src/proc)
	sys.TriggerSessionReady(s)
	sys.StartDaemons()

	// 3. Success Message & Wait
	sys.LogInfo("%s is online! (ID: %s) (PID: %d)", s.State.User.Username, s.State.User.ID, pid)
	<-shutdownChan
	if !silent {
		fmt.Println()
	}
	sys.LogInfo("Shutting down %s...", s.State.User.Username)

	return nil
}
