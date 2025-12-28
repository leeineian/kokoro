```mermaid
flowchart TB
    subgraph Entry["Entry Point"]
        Main["main.go<br/>━━━━━━━━━<br/>Parallel Startup<br/>Silent Mode Flag"]
    end

    subgraph Core["Core Systems (src/sys)"]
        Config["config.go<br/>━━━━━━━━━<br/>Environment Logic<br/>Custom Prefixes"]
        Database["database.go<br/>━━━━━━━━━<br/>SQLite WAL Mode<br/>• reminders<br/>• guild_configs<br/>• bot_config<br/>• loop_channels"]
        Loader["loader.go<br/>━━━━━━━━━<br/>Bulk Registration<br/>Interaction Router<br/>V2 Component Logic"]
        Logger["logger.go<br/>━━━━━━━━━<br/>Leveled Logging<br/>Daemon Tracking<br/>Color Formatting"]
        Components["components.go<br/>━━━━━━━━━<br/>Discord V2 System<br/>• MediaGalleries<br/>• Sections / Files<br/>• TextDisplays"]
    end

    subgraph Commands["Commands (src/cmd)"]
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

    subgraph Daemons["Background Daemons (src/proc)"]
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
    Loader --> Components
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
    class Config,Database,Loader,Logger,Components coreStyle
    class CatFact,CatImage,CatSay,DebugStats,DebugEcho,DebugStatus,DebugRoleColor,DebugLoop,ReminderSet,ReminderList,UndertextGen cmdStyle
    class ReminderScheduler,StatusRotator,RoleColorRotator,LoopRotator daemonStyle
    class Discord,CatAPI,UndertaleAPI externalStyle
```

```
minder/
|
├── main.go                       # Go entry point
├── go.mod                        # Go module dependencies
├── go.sum                        # Go dependency checksums
|
└──src/
   |
   ├──cmd/
   |  |
   │  ├── cat...go                # /cat command registration & shared logic
   │  ├── cat.fact.go             # Cat fact (catfact.ninja) handler
   │  ├── cat.image.go            # Cat image (thecatapi.com) handler
   │  ├── cat.say.go              # ASCII cowsay-style cat generator
   │  |
   │  ├── debug...go              # /debug command registration & shared logic
   │  ├── debug.echo.go           # Basic echoed message
   │  ├── debug.loop.go           # Webhook loop manager
   │  ├── debug.rolecolor.go      # RGB role color cycle configuration
   │  ├── debug.stats.go          # Live system metrics
   │  ├── debug.status.go         # Presence visibility configuration
   │  |
   │  ├── reminder...go           # /reminder command registration & shared logic
   │  ├── reminder.set.go         # Natural language time parsing & storage
   │  ├── reminder.list.go        # Interactive view of pending reminders
   │  |
   │  ├── undertext...go          # /undertext command registration & shared logic
   │  └── undertext.handler.go    # Demirramon API bridge (Static/Animated)
   |
   ├──proc/
   |  |
   │  ├── looprotator.go          # Webhook loop daemon
   │  ├── reminderscheduler.go    # Reminder notification daemon
   │  ├── rolecolorrotator.go     # Role color cycle daemon
   │  └── statusrotator.go        # Status cycle daemon
   |
   └──sys/
      |
      ├── components.go           # Discord V2 Component wrappers
      ├── config.go               # Environment validation
      ├── database.go             # SQLite database layer
      ├── loader.go               # Session creation & command registration
      └── logger.go               # Prefix-based color logging
```