const db = require('../setup');
const ConsoleLogger = require('../../log/consoleLogger');

// Cache prepared statements for performance
const stmt = {
    addReminder: db.prepare('INSERT INTO reminders (userId, channelId, message, dueAt, deliveryType, createdAt) VALUES (?, ?, ?, ?, ?, ?)'),
    getReminders: db.prepare('SELECT * FROM reminders WHERE userId = ? ORDER BY dueAt ASC'),
    deleteReminder: db.prepare('DELETE FROM reminders WHERE id = ?'),
    deleteAllReminders: db.prepare('DELETE FROM reminders WHERE userId = ?'),
    getAllPending: db.prepare('SELECT * FROM reminders ORDER BY dueAt ASC'),
    getCountAll: db.prepare('SELECT COUNT(*) as count FROM reminders'),
    getCountUser: db.prepare('SELECT COUNT(*) as count FROM reminders WHERE userId = ?')
};

module.exports = {
    addReminder: (userId, channelId, message, dueAt, deliveryType = 'dm') => {
        try {
            const info = stmt.addReminder.run(userId, channelId, message, dueAt, deliveryType, Date.now());
            return info.lastInsertRowid;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to add reminder:', error);
            throw new Error('Database operation failed: addReminder');
        }
    },

    getReminders: (userId) => {
        try {
            return stmt.getReminders.all(userId);
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to get reminders:', error);
            return [];
        }
    },

    deleteReminder: (id) => {
        try {
            const info = stmt.deleteReminder.run(id);
            return info.changes;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to delete reminder:', error);
            return 0;
        }
    },

    deleteAllReminders: (userId) => {
        try {
            const info = stmt.deleteAllReminders.run(userId);
            return info.changes;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to delete all reminders:', error);
            return 0;
        }
    },

    getAllPendingReminders: () => {
        try {
            return stmt.getAllPending.all();
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to get pending reminders:', error);
            return [];
        }
    },

    getRemindersCount: (userId = null) => {
        try {
            const row = userId ? stmt.getCountUser.get(userId) : stmt.getCountAll.get();
            return row ? row.count : 0;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to get reminders count:', error);
            return 0;
        }
    },

    /**
     * Marks a reminder as sent (for idempotency).
     * @param {number|string} id - Reminder ID
     * @returns {boolean} - True if marked successfully, false if already sent
     */
    markReminderAsSent: (id) => {
        try {
            const stmt = db.prepare('UPDATE reminders SET sent_at = ? WHERE id = ? AND sent_at IS NULL');
            const info = stmt.run(Date.now(), id);
            return info.changes > 0;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to mark reminder as sent:', error);
            return false;
        }
    },

    /**
     * Resets sent status for retry (when delivery fails).
     * @param {number|string} id - Reminder ID
     * @returns {number} - Number of rows updated
     */
    resetReminderSentStatus: (id) => {
        try {
            const stmt = db.prepare('UPDATE reminders SET sent_at = NULL WHERE id = ?');
            const info = stmt.run(id);
            return info.changes;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to reset sent status:', error);
            return 0;
        }
    },

    /**
     * Resets ALL reminder sent statuses (clears locks).
     * Useful on startup to fix "stuck" reminders from a crash.
     */
    resetAllReminderLocks: () => {
        try {
            const stmt = db.prepare('UPDATE reminders SET sent_at = NULL WHERE sent_at IS NOT NULL');
            const info = stmt.run();
            return info.changes;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to reset all reminder locks:', error);
            return 0;
        }
    }
};

