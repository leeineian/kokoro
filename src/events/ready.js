const { Events } = require('discord.js');
const ConsoleLogger = require('../utils/consoleLogger');
const { performance } = require('perf_hooks');
const webhookPinger = require('../scripts/webhookPinger');
const statusRotator = require('../scripts/statusRotator');
const randomRoleColor = require('../scripts/randomRoleColor');
const db = require('../utils/database');
const reminder = require('../commands/reminder');

module.exports = {
    name: Events.ClientReady,
    once: true,
    async execute(client) {
        ConsoleLogger.success('Start', `Ready! Logged in as ${client.user.tag} (Startup: ${performance.now().toFixed(2)}ms)`);
        
        // Start Background Scripts
        statusRotator.start(client);
        randomRoleColor.start(client);

        // Initialize Ping Persistence
        await webhookPinger.initialize(client);

        // Restore Reminders
        try {
            // Synchronous call - no await
            const pending = db.getAllPendingReminders();
            ConsoleLogger.info('Reminders', `Restoring ${pending.length} pending reminders...`);
            
            let restoredCount = 0;

            for (const r of pending) {
                // Restore using safe scheduler
                reminder.scheduleReminder(client, r.userId, r.channelId, r.message, r.id, r.deliveryType, r.dueAt);
                restoredCount++;
            }
            ConsoleLogger.success('Reminders', `Restored ${restoredCount} reminders.`);
        } catch (err) {
            ConsoleLogger.error('Reminders', 'Failed to restore reminders:', err);
        }
    },
};
