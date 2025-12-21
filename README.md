# minder

An app for The Mindscape discord server.

## setup

1.  **clone/open project**: ensure you are in the `minder` directory.
2.  **install dependencies**:
    ```bash
    bun install
    ```
3.  **configure environment**:
    - make an `.env` file.
    - fill in your `DISCORD_TOKEN` and `CLIENT_ID`.
    - fill in `GUILD_ID` and `ROLE_ID` (required for role color feature).

## running the app

1.  **register commands**:
    run this once (or whenever you change commands):
    ```bash
    bun run sync
    ```
    if you provided a `GUILD_ID`, commands will appear immediately in that server. if not, it may take up to an hour to appear globally.

2.  **start the app**:
    ```bash
    bun start
    ```
    or for development with hot-reload:
    ```bash
    bun run dev
    ```

## architecture

### file structure
- `src/commands`: Slash command definitions.
- `src/events`: Event listeners (ready, interactionCreate).
- `src/scripts`: Background services (StatusRotator, WebhookPinger, AI).
- `src/utils`: Shared utilities.
    - `auditLogger.js`: Discord logging.
    - `consoleLogger.js`: Terminal logging.
    - `database.js`: Centralized DB repository access.
    - `reminderScheduler.js`: Robust reminder handling logic.

### key systems

#### database (`bun:sqlite`)
the bot uses a local SQLite database with WAL mode enabled for performance. access is abstracted through repositories in `src/utils/db/repo`.

#### reminder scheduler
reminders are persistent. on startup, `src/events/ready.js` loads all pending reminders from the DB and re-schedules them using `reminderScheduler.js`. this ensures no reminders are lost during restarts.

#### webhook pinger
a stress-testing tool (`/debug webhook-pinger`) designed to handle rate limits and optimize throughput using parallel execution.

#### ai chat
integrated with `LLM7.io`, handling context-aware conversations with memory barriers and dynamic context sizing.

## test usage

in Discord, type:
`/say message:Hello World`

the app will reply with an ephemeral message "Hello World".

## process management

### correctly stopping the app
to stop the app, click inside the terminal running it and press **`Ctrl + C`**. this sends a shutdown signal to the process.

### to kill all running app processes:
```bash
# Linux/Mac
bun run stop
```
