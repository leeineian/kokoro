const sqlite3 = require('sqlite3').verbose();
const path = require('path');

const dbPath = path.resolve(__dirname, '../reminders.db');
const db = new sqlite3.Database(dbPath);

// Initialize Table
db.serialize(() => {
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
    `, (err) => {
        if (err) console.error('DB Initialization Error:', err);
    });
});

module.exports = {
    addReminder: (userId, channelId, message, dueAt, deliveryType = 'dm') => {
        return new Promise((resolve, reject) => {
            const stmt = db.prepare('INSERT INTO reminders (userId, channelId, message, dueAt, deliveryType, createdAt) VALUES (?, ?, ?, ?, ?, ?)');
            stmt.run(userId, channelId, message, dueAt, deliveryType, Date.now(), function(err) {
                if (err) reject(err);
                else resolve(this.lastID);
            });
            stmt.finalize();
        });
    },

    getReminders: (userId) => {
        return new Promise((resolve, reject) => {
            db.all('SELECT * FROM reminders WHERE userId = ? ORDER BY dueAt ASC', [userId], (err, rows) => {
                if (err) reject(err);
                else resolve(rows);
            });
        });
    },

    deleteReminder: (id) => {
        return new Promise((resolve, reject) => {
            db.run('DELETE FROM reminders WHERE id = ?', [id], function(err) {
                if (err) reject(err);
                else resolve(this.changes);
            });
        });
    },

    getAllPendingReminders: () => {
        return new Promise((resolve, reject) => {
            db.all('SELECT * FROM reminders', [], (err, rows) => {
                if (err) reject(err);
                else resolve(rows);
            });
        });
    }
};
