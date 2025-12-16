# minder

An app for The Mindscape discord server.

## Setup

1.  **Clone/Open Project**: Ensure you are in the `minder` directory.
2.  **Install Dependencies**:
    ```bash
    npm install
    ```
3.  **Configure Environment**:
    - Copy `.env.example` to `.env`.
    - Fill in your `DISCORD_TOKEN` and `CLIENT_ID`.
    - (Optional) Fill in `GUILD_ID` for faster command registration during development.

## Running the Bot

1.  **Register Commands**:
    Run this once (or whenever you change commands):
    ```bash
    npm run deploy
    ```
    If you provided a `GUILD_ID`, commands will appear immediately in that server. If not, it may take up to an hour to appear globally.

2.  **Start the Bot**:
    ```bash
    npm start
    ```

## Test usage

In Discord, type:
`/say message:Hello World`

The bot will reply with an ephemeral message "Hello World".
