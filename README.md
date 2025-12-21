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
    bun run deploy
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

## test usage

in Discord, type:
`/say message:Hello World`

the app will reply with an ephemeral message "Hello World".

## process management

### correctly stopping the app
to stop the app, click inside the terminal running it and press **`Ctrl + C`**. This sends a shutdown signal to the process.

### to kill all running app processes:
```bash
# Linux/Mac
bun run stop
```
