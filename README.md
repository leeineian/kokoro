```mermaid
flowchart TB
    subgraph Entry["Entry Point"]
        Main["main.go<br/>━━━━━━━━━<br/>Parallel Startup<br/>Silent Mode Flag"]
    end

    subgraph Core["Core Systems (sys)"]
        Database["database.go<br/>━━━━━━━━━<br/>Environment & SQLite Layer<br/>• Standardized Snowflake IDs<br/>• WAL-Mode Performance"]
        Loader["loader.go<br/>━━━━━━━━━<br/>Bulk Registration<br/>Interaction Router<br/>V2 Component Support"]
        Logger["logger.go<br/>━━━━━━━━━<br/>Structured slog Logging<br/>Dynamic AST Discovery<br/>Custom Colored Handler"]
    end

    subgraph Commands["Commands (home)"]
        direction TB
        
        subgraph CatCmd["/cat"]
            CatFact["fact (API)"]
            CatImage["image (API)"]
            CatSay["say (ANSI ASCII)"]
            CatStats["stats (System)"]
        end
        
        subgraph SessionCmd["/session"]
            SessionReboot["reboot (Process)"]
            SessionShutdown["shutdown (Graceful)"]
            SessionStats["stats (Metrics)"]
            SessionStatus["status (Presence)"]
            SessionConsole["console (Log Viewer)"]
        end

        subgraph LoopCmd["/loop"]
            LoopStats["stats (State)"]
            LoopSet["set (Target)"]
            LoopStart["start (Stress)"]
            LoopStop["stop (Cleanup)"]
            LoopErase["erase (Category)"]
        end

        subgraph ReminderCmd["/reminder"]
            ReminderSet["set (Natural Time)"]
            ReminderList["list (Interactive)"]
            ReminderStats["stats (Summary)"]
        end

        subgraph RoleColorCmd["/rolecolor"]
            RoleColorSet["set (Binding)"]
            RoleColorReset["reset (Cleanup)"]
            RoleColorRefresh["refresh (Immediate)"]
            RoleColorStats["stats (Config)"]
        end
        
        subgraph UtilityCmd["Utilities"]
            UndertextCmd["/undertext (Sprites)"]
        end
    end

    subgraph Daemons["Background Daemons (proc)"]
        ReminderManager["reminder.manager.go<br/>━━━━━━━━━<br/>10s Poll Interval<br/>Context-Safe Queries"]
        StatusManager["status.manager.go<br/>━━━━━━━━━<br/>15-60s Cycle<br/>Live System Metrics"]
        RoleColorManager["rolecolor.manager.go<br/>━━━━━━━━━<br/>RGB Cycle Logic<br/>Snowflake-Safe Mapping"]
        LoopManager["loop.manager.go<br/>━━━━━━━━━<br/>Webhook Looper<br/>State-Aware Scheduling"]
    end

    subgraph External["External Services"]
        Discord["Discord API v10"]
        CatAPI["Cat APIs<br/>• fact/image"]
        UndertaleAPI["Demirramon API<br/>• Box Generator"]
    end

    %% Entry connections
    Main --> Database
    Main --> Loader
    
    %% Core system connections
    Loader --> Logger
    Database --> Logger

    %% Command registration
    Loader --> CatCmd
    Loader --> SessionCmd
    Loader --> LoopCmd
    Loader --> ReminderCmd
    Loader --> RoleColorCmd
    Loader --> UtilityCmd

    %% Daemon startup
    Main --> ReminderManager
    Main --> StatusManager
    Main --> RoleColorManager
    Main --> LoopManager

    %% Daemon dependencies
    ReminderManager --> Database
    RoleColorManager --> Database
    LoopManager --> Database

    %% External API connections
    CatCmd --> CatAPI
    UtilityCmd --> UndertaleAPI
    Loader --> Discord
    Daemons --> Discord

    %% Styling
    classDef entryStyle fill:#1a1a2e,stroke:#e94560,stroke-width:2px,color:#fff
    classDef coreStyle fill:#16213e,stroke:#0f3460,stroke-width:2px,color:#fff
    classDef cmdStyle fill:#1a1a2e,stroke:#4a90a4,stroke-width:2px,color:#fff
    classDef daemonStyle fill:#1a1a2e,stroke:#9b59b6,stroke-width:2px,color:#fff
    classDef externalStyle fill:#2d3436,stroke:#00b894,stroke-width:2px,color:#fff

    class Main entryStyle
    class Database,Loader,Logger coreStyle
    class CatFact,CatImage,CatSay,CatStats,SessionReboot,SessionShutdown,SessionStats,SessionStatus,SessionConsole,LoopStats,LoopSet,LoopStart,LoopStop,LoopErase,ReminderSet,ReminderList,ReminderStats,RoleColorSet,RoleColorReset,RoleColorRefresh,RoleColorStats,UndertextCmd cmdStyle
    class ReminderManager,StatusManager,RoleColorManager,LoopManager daemonStyle
    class Discord,CatAPI,UndertaleAPI externalStyle
```

```
minder/
│
├── main.go                       # Go entry point
├── go.mod                        # Go module dependencies
├── go.sum                        # Go dependency checksums
|
├── Dockerfile                    # Multi-stage build
├── docker-compose.yml            # Multi-service deployment
|
├── home/                         # [Slash Commands]
│   ├── cat...go                  # /cat router
│   ├── cat.fact.go               # /cat fact
│   ├── cat.image.go              # /cat image
│   ├── cat.say.go                # /cat say
│   ├── cat.stats.go              # /cat stats
│   ├── loop...go                 # /loop router
│   ├── loop.erase.go             # /loop erase
│   ├── loop.set.go               # /loop set
│   ├── loop.start.go             # /loop start
│   ├── loop.stop.go              # /loop stop
│   ├── loop.stats.go             # /loop stats
│   ├── reminder...go             # /reminder router
│   ├── reminder.list.go          # /reminder list
│   ├── reminder.set.go           # /reminder set
│   ├── reminder.stats.go         # /reminder stats
│   ├── rolecolor...go            # /rolecolor router
│   ├── rolecolor.refresh.go      # /rolecolor refresh
│   ├── rolecolor.reset.go        # /rolecolor reset
│   ├── rolecolor.set.go          # /rolecolor set
│   ├── rolecolor.stats.go        # /rolecolor stats
│   ├── session...go              # /session router
│   ├── session.console.go        # /session console
│   ├── session.reboot.go         # /session reboot
│   ├── session.shutdown.go       # /session shutdown
│   ├── session.stats.go          # /session stats
│   ├── session.status.go         # /session status
│   └── undertext.go              # /undertext
│
├── proc/                         # [Background Daemons]
│   ├── loop.manager.go           # Webhook loop manager
│   ├── reminder.manager.go       # Reminder notification daemon
│   ├── rolecolor.manager.go      # Role color cycle daemon
│   └── status.manager.go         # Status cycle daemon
│
└── sys/                          # [Core Systems]
    ├── database.go               # Configuration & SQLite layer
    ├── loader.go                 # Session creation & registration
    └── logger.go                 # Leveled Logging & AST Discovery
```