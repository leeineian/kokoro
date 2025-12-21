const { MessageFlags } = require('discord.js');
const V2Builder = require('../utils/core/components');
const db = require('../utils/core/database');
const ConsoleLogger = require('../utils/log/consoleLogger');
const { setLongTimeout } = require('../utils/core/timer');


// Track active timeouts for cancellation
const activeTimeouts = new Map(); // Map<reminderId, timeoutId>

module.exports = {
    /**
     * Schedules a reminder for delivery.
     * Handles delays larger than setTimeout limit (24.8 days) by recursively rescheduling.
     * 
     * @param {import('discord.js').Client} client 
     * @param {string} userId 
     * @param {string} channelId 
     * @param {string} message 
     * @param {number|string} dbId 
     * @param {string} deliveryType 'dm' or 'channel'
     * @param {number} dueAt Timestamp
     */
    scheduleReminder(client, userId, channelId, message, dbId, deliveryType, dueAt) {
        const now = Date.now();
        const delay = Math.max(0, dueAt - now); // Ensure non-negative

        const timer = setLongTimeout(() => {
            this.sendReminder(client, userId, channelId, message, dbId, deliveryType);
        }, delay);
        
        activeTimeouts.set(dbId, timer);
        
        // Log info for long reminders
        if (delay > 2147483647) {
             const days = (delay / 86400000).toFixed(1);
             ConsoleLogger.info('ReminderScheduler', `Scheduled long-term reminder ${dbId} for ${days} days from now.`);
        }
    },

    /**
     * Cancels a scheduled reminder.
     * @param {number|string} dbId - Reminder ID
     */
    cancelReminder(dbId) {
        const timer = activeTimeouts.get(dbId);
        if (timer) {
            timer.cancel(); // Use the object's cancel method
            activeTimeouts.delete(dbId);
            ConsoleLogger.info('ReminderScheduler', `Cancelled reminder ${dbId}`);
        }
    },

    /**
     * Sends the reminder payload and removes it from the database.
     * Implements idempotency to prevent duplicate sends on bot restarts.
     */
    async sendReminder(client, userId, channelId, message, dbId, deliveryType = 'dm') {
        try {
            // Mark as sending (optimistic lock)
            // This also acts as idempotency check - returns false if already sent
            const marked = db.markReminderAsSent(dbId);
            if (!marked) {
                ConsoleLogger.warn('ReminderScheduler', 
                    `Reminder ${dbId} already sent or being sent by another process, skipping.`);
                return;
            }

            // --- VALIDATE IDs to prevent API Errors (Fix for "Invalid Form Body") ---
            const isSnowflake = (id) => /^\d{17,19}$/.test(id);

            if (!isSnowflake(userId)) {
                ConsoleLogger.warn('ReminderScheduler', `Reminder ${dbId} has invalid userId "${userId}". Deleting corrupt reminder.`);
                db.deleteReminder(dbId); // Auto-cleanup
                activeTimeouts.delete(dbId);
                return;
            }

            if (channelId && !isSnowflake(channelId)) {
                ConsoleLogger.warn('ReminderScheduler', `Reminder ${dbId} has invalid channelId "${channelId}". Deleting corrupt reminder.`);
                db.deleteReminder(dbId); // Auto-cleanup
                activeTimeouts.delete(dbId);
                return;
            }
            // ---------------------------------------------------------------------

            const reminderText = `⏰ **Time's Up, <@${userId}>!**\nReminder: "${message}"`;
            
            const v2Container = V2Builder.container([
                V2Builder.textDisplay(reminderText),
                V2Builder.actionRow([
                    V2Builder.button('Dismiss', 'dismiss_message', 4)
                ])
            ]);

            let deliverySuccess = false;

            if (deliveryType === 'channel' && channelId) {
                try {
                    const channel = await client.channels.fetch(channelId);
                    if (channel) {
                        await channel.send({ 
                            content: null,
                            flags: MessageFlags.IsComponentsV2,
                            components: [v2Container] 
                        });
                        deliverySuccess = true;
                    }

                } catch (channelError) {
                    ConsoleLogger.error('ReminderScheduler', `Channel Delivery Failed (Channel: ${channelId}):`, channelError);
                }
            } else {
                // DM Delivery
                try {
                    const user = await client.users.fetch(userId);
                    if (user) {
                         await user.send({ 
                            content: null,
                            components: [v2Container],
                            flags: MessageFlags.IsComponentsV2
                        });
                        deliverySuccess = true;
                    }
                } catch (dmError) {
                    ConsoleLogger.error('ReminderScheduler', `DM Delivery Failed (User: ${userId}):`, dmError);
                    
                    // Fallback to channel if DM fails
                    if (channelId) {
                        try {
                            const channel = await client.channels.fetch(channelId);
                            if (channel) {
                                // Add fallback text to container
                                const fallbackContainer = V2Builder.container([
                                    V2Builder.textDisplay(`<@${userId}> I couldn't DM you.`),
                                    ...v2Container.components
                                ]);

                                await channel.send({ 
                                    content: null,
                                    flags: MessageFlags.IsComponentsV2,
                                    components: [fallbackContainer] 
                                });
                                deliverySuccess = true;
                            }
                        } catch (channelError) {
                            ConsoleLogger.error('ReminderScheduler', `Fallback Delivery Failed (Channel: ${channelId}):`, channelError);
                        }
                    }
                }
            }

            if (deliverySuccess) {
                // Success - delete from database
                db.deleteReminder(dbId);
                activeTimeouts.delete(dbId); // Clean up timeout tracker
                ConsoleLogger.success('ReminderScheduler', `Reminder ${dbId} delivered and removed from database.`);
            } else {
                // Delivery failed - reset sent_at to allow retry on next restore
                db.resetReminderSentStatus(dbId);
                ConsoleLogger.error('ReminderScheduler', 
                    `Reminder ${dbId} delivery failed, sent_at reset for retry on next bot restart.`);
            }

        } catch (error) {
            ConsoleLogger.error('ReminderScheduler', `Failed to deliver reminder ${dbId}:`, error);
            // Reset sent_at on error to allow retry
            try {
                db.resetReminderSentStatus(dbId);
            } catch (resetError) {
                ConsoleLogger.error('ReminderScheduler', `Failed to reset sent_at for reminder ${dbId}:`, resetError);
}
        }
    }
};
