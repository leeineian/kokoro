#!/bin/bash

# Configuration
# Path to script directory
APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# App name based on folder name
APP_NAME="$(basename "$APP_DIR")"
# Executable name (default to app name)
APP_EXEC="./$APP_NAME"
# PID file (staying compatible with .bot.pid but allowing easy change)
PID_FILE=".bot.pid"
# Log file
LOG_FILE="$APP_NAME.log"

# Navigate to application directory
cd "$APP_DIR" || exit 1

# Check if application is running
if [ -f "$PID_FILE" ]; then
    PID=$(cat "$PID_FILE")
    if ps -p "$PID" > /dev/null 2>&1; then
        echo "$APP_NAME is running (PID: $PID). Stopping..."
        kill -TERM "$PID"
        
        # Wait for process to exit
        for i in {1..10}; do
            if ! ps -p "$PID" > /dev/null 2>&1; then
                echo "Stopped."
                exit 0
            fi
            sleep 0.5
        done
        
        echo "Process didn't stop gracefully, sending SIGKILL..."
        kill -9 "$PID"
        rm -f "$PID_FILE"
        exit 0
    else
        echo "Stale PID file found for $APP_NAME. Cleaning up..."
        rm -f "$PID_FILE"
    fi
fi

# Ensure executable exists
if [ ! -f "$APP_EXEC" ]; then
    echo "Error: Executable $APP_EXEC not found in $APP_DIR"
    exit 1
fi

# Not running, start it detached
echo "Starting $APP_NAME..."
nohup "$APP_EXEC" > "$LOG_FILE" 2>&1 &
echo "Started in background (PID: $!)."
