// load .env
const { Database } = require('bun:sqlite');
const path = require('path');

// Resolve path to data.db (root directory)
const dbPath = path.resolve(__dirname, '../../../data.db');

// Enable WAL mode (Write-Ahead Logging) for better concurrency and performance
const db = new Database(dbPath);
db.run('PRAGMA journal_mode = WAL');

// Initialize Tables
// 1. Reminders
db.run(`
    CREATE TABLE IF NOT EXISTS reminders (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        userId TEXT NOT NULL,
        channelId TEXT,
        message TEXT NOT NULL,
        dueAt INTEGER NOT NULL,
        deliveryType TEXT DEFAULT 'dm',
        createdAt INTEGER NOT NULL
    )
`);

db.run('CREATE INDEX IF NOT EXISTS idx_userId ON reminders(userId)');
db.run('CREATE INDEX IF NOT EXISTS idx_dueAt ON reminders(dueAt)');

// 2. Guild Configs
db.run(`
    CREATE TABLE IF NOT EXISTS guild_configs (
        guildId TEXT PRIMARY KEY,
        config TEXT NOT NULL    -- JSON string for flexibility
    )
`);

console.log('[Database] Initialized with bun:sqlite (data.db).');

module.exports = db;
