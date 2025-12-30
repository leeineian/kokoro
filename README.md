```mermaid
flowchart TB
    subgraph Entry["Entry Point"]
        Main["main.go<br/>━━━━━━━━━<br/>Parallel Startup<br/>Silent Mode Flag"]
    end

    subgraph Core["Core Systems (sys)"]
        Config["config.go<br/>━━━━━━━━━<br/>Environment Logic<br/>Custom Prefixes"]
        Database["database.go<br/>━━━━━━━━━<br/>SQLite WAL Mode<br/>• reminders<br/>• guild_configs<br/>• bot_config<br/>• loop_channels"]
        Loader["loader.go<br/>━━━━━━━━━<br/>Bulk Registration<br/>Interaction Router<br/>Session Management"]
        Logger["logger.go<br/>━━━━━━━━━<br/>Leveled Logging<br/>Daemon Tracking<br/>Color Formatting"]
    end

    subgraph Commands["Commands (home)"]
        direction TB
        
        subgraph CatCmd["/cat"]
            CatFact["fact (API)"]
            CatImage["image (API)"]
            CatSay["say (ANSI ASCII)"]
        end
        
        subgraph DebugCmd["/debug"]
            DebugStats["stats / ping"]
            DebugEcho["echo"]
            DebugStatus["status config"]
            DebugRoleColor["rolecolor cycle"]
            DebugLoop["webhook stress"]
        end
        
        subgraph ReminderCmd["/reminder"]
            ReminderSet["set (Natural Time)"]
            ReminderList["list (Interactive)"]
        end
        
        subgraph UndertextCmd["/undertext"]
            UndertextGen["Generated Sprites<br/>Animated GIFs<br/>Autocompletion"]
        end
    end

    subgraph Daemons["Background Daemons (proc)"]
        ReminderScheduler["reminderscheduler.go<br/>━━━━━━━━━<br/>10s Poll Interval<br/>DM/Channel Alerts"]
        StatusRotator["statusrotator.go<br/>━━━━━━━━━<br/>15-60s Random Cycle<br/>5 Dynamic States"]
        RoleColorRotator["rolecolorrotator.go<br/>━━━━━━━━━<br/>RGB Cycle Logic<br/>Guild-Specific"]
        LoopRotator["looprotator.go<br/>━━━━━━━━━<br/>Webhook Looper<br/>Channel Spammer"]
    end

    subgraph External["External Services"]
        Discord["Discord API v10"]
        CatAPI["Cat APIs<br/>• fact/image"]
        UndertaleAPI["Demirramon API<br/>• Box Generator"]
    end

    %% Entry connections
    Main --> Config
    Main --> Database
    Main --> Loader
    
    %% Core system connections
    Loader --> Logger
    Database --> Logger

    %% Command registration
    Loader --> CatCmd
    Loader --> DebugCmd
    Loader --> ReminderCmd
    Loader --> UndertextCmd

    %% Daemon startup
    Main --> ReminderScheduler
    Main --> StatusRotator
    Main --> RoleColorRotator
    Main --> LoopRotator

    %% Daemon dependencies
    ReminderScheduler --> Database
    RoleColorRotator --> Database
    LoopRotator --> Database

    %% External API connections
    CatCmd --> CatAPI
    UndertextCmd --> UndertaleAPI
    Loader --> Discord
    Daemons --> Discord

    %% Styling
    classDef entryStyle fill:#1a1a2e,stroke:#e94560,stroke-width:2px,color:#fff
    classDef coreStyle fill:#16213e,stroke:#0f3460,stroke-width:2px,color:#fff
    classDef cmdStyle fill:#1a1a2e,stroke:#4a90a4,stroke-width:2px,color:#fff
    classDef daemonStyle fill:#1a1a2e,stroke:#9b59b6,stroke-width:2px,color:#fff
    classDef externalStyle fill:#2d3436,stroke:#00b894,stroke-width:2px,color:#fff

    class Main entryStyle
    class Config,Database,Loader,Logger coreStyle
    class CatFact,CatImage,CatSay,DebugStats,DebugEcho,DebugStatus,DebugRoleColor,DebugLoop,ReminderSet,ReminderList,UndertextGen cmdStyle
    class ReminderScheduler,StatusRotator,RoleColorRotator,LoopRotator daemonStyle
    class Discord,CatAPI,UndertaleAPI externalStyle
```

```
minder/
│
├── main.go                       # Go entry point
├── go.mod                        # Go module dependencies
├── go.sum                        # Go dependency checksums
│
├── home/                         # [Discord Commands]
│   ├── cat...go                  # /cat command logic
│   ├── cat.fact.go               # /cat fact (catfact.ninja) handler
│   ├── cat.image.go              # /cat image (thecatapi.com) handler
│   ├── cat.say.go                # /cat say (ANSI ASCII) handler
│   ├── debug...go                # /debug command logic
│   ├── debug.echo.go             # /debug echo (ANSI ASCII) handler
│   ├── debug.loop.go             # /debug loop (Webhook) handler
│   ├── debug.rolecolor.go        # /debug rolecolor (RGB) handler
│   ├── debug.stats.go            # /debug stats (Live System Metrics) handler
│   ├── debug.status.go           # /debug status (Presence Visibility) handler
│   ├── reminder...go             # /reminder command logic
│   ├── reminder.set.go           # /reminder set (Natural Language Time) handler
│   ├── reminder.list.go          # /reminder list (Interactive View) handler
│   ├── undertext...go            # /undertext command logic
│   └── undertext.handler.go      # /undertext (Demirramon API bridge) handler
│
├── proc/                         # [Background Daemons]
│   ├── looprotator.go            # Webhook loop daemon
│   ├── reminderscheduler.go      # Reminder notification daemon
│   ├── rolecolorrotator.go       # Role color cycle daemon
│   └── statusrotator.go          # Status cycle daemon
│
├── sys/                          # [Core Systems]
│   ├── config.go                 # Environment configuration
│   ├── database.go               # SQLite database layer
│   ├── loader.go                 # Session creation & command registration
│   └── logger.go                 # Prefix-based color logging
└── README.md                     # Project documentation
```