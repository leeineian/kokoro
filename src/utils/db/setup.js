// load .env
const { Database } = require('bun:sqlite');
const path = require('path');
const { runMigrations } = require('./migrations');

// Resolve path to database
const isTest = process.env.NODE_ENV === 'test';
const dbName = isTest ? 'test.db' : 'data.db';
const dbPath = path.resolve(__dirname, `../../../${dbName}`);

// Enable WAL mode (Write-Ahead Logging) for better concurrency and performance
const db = new Database(dbPath);
db.run('PRAGMA journal_mode = WAL');

// Verify WAL Mode
const journalMode = db.prepare('PRAGMA journal_mode').get();

// Run database migrations
runMigrations(db);

const ConsoleLogger = require('../log/consoleLogger');
// Suppress logs if LOG_LEVEL is silent OR if running in test mode
if (process.env.LOG_LEVEL !== 'silent' && process.env.NODE_ENV !== 'test') {
    ConsoleLogger.info('Database', `Initialized with bun:sqlite (data.db) | Mode: ${journalMode ? journalMode.journal_mode.toUpperCase() : 'UNKNOWN'}`);
}

// Graceful cleanup on process exit
process.on('beforeExit', () => {
    try {
        db.close();
        ConsoleLogger.info('Database', 'Connection closed gracefully.');
    } catch (err) {
        ConsoleLogger.error('Database', 'Error closing database:', err);
    }
});

module.exports = db;


