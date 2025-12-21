const { Events } = require('discord.js');
const ConsoleLogger = require('../utils/log/consoleLogger');
const { performance } = require('perf_hooks');
const webhookLooper = require('../daemons/webhookLooper');
const statusRotator = require('../daemons/statusRotator');
const randomRoleColor = require('../daemons/randomRoleColor');
const db = require('../utils/core/database');
const reminderScheduler = require('../daemons/reminderScheduler');

module.exports = {
    name: Events.ClientReady,
    once: true,
    async execute(client) {
        ConsoleLogger.success('Start', `Ready! Logged in as ${client.user.tag} (Startup: ${performance.now().toFixed(2)}ms)`);
        
        // Start Background Scripts
        statusRotator.start(client);
        randomRoleColor.start(client);

        // Initialize Loop Persistence
        await webhookLooper.initialize(client);

        // Restore Reminders
        try {
            // Clear any stuck locks from previous crashes
            const clearedLocks = db.resetAllReminderLocks();
            if (clearedLocks > 0) ConsoleLogger.warn('Reminders', `Cleared ${clearedLocks} stuck reminder locks.`);

            // Synchronous call - no await
            const pending = db.getAllPendingReminders();
            ConsoleLogger.info('Reminders', `Restoring ${pending.length} pending reminders...`);
            
            let restoredCount = 0;

            for (const r of pending) {
                // Restore using safe scheduler
                reminderScheduler.scheduleReminder(client, r.userId, r.channelId, r.message, r.id, r.deliveryType, r.dueAt);
                restoredCount++;
            }
            ConsoleLogger.success('Reminders', `Restored ${restoredCount} reminders.`);
        } catch (err) {
            ConsoleLogger.error('Reminders', `Failed to restore reminders. Database returned ${pending?.length || 0} pending reminders.`, err);
        }
    },
};
