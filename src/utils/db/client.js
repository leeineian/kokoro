// load .env
const { Database } = require('bun:sqlite');
const path = require('path');

// Resolve path to data.db (root directory)
const dbPath = path.resolve(__dirname, '../../../data.db');

// Enable WAL mode (Write-Ahead Logging) for better concurrency and performance
const db = new Database(dbPath);
db.run('PRAGMA journal_mode = WAL');

// Verify WAL Mode
const journalMode = db.prepare('PRAGMA journal_mode').get();
if (journalMode && journalMode.journal_mode === 'wal') {
}

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

// 3. User AI Prompts
db.run(`
    CREATE TABLE IF NOT EXISTS user_ai_prompts (
        userId TEXT PRIMARY KEY,
        prompt TEXT NOT NULL
    )
`);

// 4. AI Memory Barriers
db.run(`
    CREATE TABLE IF NOT EXISTS ai_memory_barriers (
        channelId TEXT PRIMARY KEY,
        timestamp INTEGER NOT NULL
    )
`);

// 5. Ping Categories
db.run(`
    CREATE TABLE IF NOT EXISTS ping_categories (
        categoryId TEXT PRIMARY KEY,
        categoryName TEXT NOT NULL
    )
`);

const ConsoleLogger = require('../consoleLogger');
ConsoleLogger.info('Database', `Initialized with bun:sqlite (data.db) | Mode: ${journalMode ? journalMode.journal_mode.toUpperCase() : 'UNKNOWN'}`);

module.exports = db;
