const db = require('../client');

module.exports = {
    addReminder: (userId, channelId, message, dueAt, deliveryType = 'dm') => {
        const stmt = db.prepare('INSERT INTO reminders (userId, channelId, message, dueAt, deliveryType, createdAt) VALUES (?, ?, ?, ?, ?, ?)');
        const info = stmt.run(userId, channelId, message, dueAt, deliveryType, Date.now());
        return info.lastInsertRowid;
    },

    getReminders: (userId) => {
        return db.prepare('SELECT * FROM reminders WHERE userId = ? ORDER BY dueAt ASC').all(userId);
    },

    deleteReminder: (id) => {
        const info = db.prepare('DELETE FROM reminders WHERE id = ?').run(id);
        return info.changes;
    },

    deleteAllReminders: (userId) => {
        const info = db.prepare('DELETE FROM reminders WHERE userId = ?').run(userId);
        return info.changes;
    },

    getAllPendingReminders: () => {
        return db.prepare('SELECT * FROM reminders ORDER BY dueAt ASC').all();
    },

    getRemindersCount: () => {
        const row = db.prepare('SELECT COUNT(*) as count FROM reminders').get();
        return row.count;
    }
};
