const { MessageFlags } = require('discord.js');
const V2Builder = require('./components');
const db = require('./database');
const ConsoleLogger = require('./consoleLogger');

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
        const delay = dueAt - now;
        const MAX_DELAY = 2147483647; // 2^31 - 1 (~24.8 days)

        if (delay <= 0) {
            // Send immediately
            this.sendReminder(client, userId, channelId, message, dbId, deliveryType);
        } else if (delay > MAX_DELAY) {
            // Too long for a single setTimeout, wait MAX_DELAY then check again
            setTimeout(() => {
                this.scheduleReminder(client, userId, channelId, message, dbId, deliveryType, dueAt);
            }, MAX_DELAY);
        } else {
            // Safe to schedule directly
            setTimeout(() => {
                this.sendReminder(client, userId, channelId, message, dbId, deliveryType);
            }, delay);
        }
    },

    /**
     * Sends the reminder payload and removes it from the database.
     */
    async sendReminder(client, userId, channelId, message, dbId, deliveryType = 'dm') {
        try {
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
                                await channel.send({ 
                                    content: `<@${userId}> I couldn't DM you.`,
                                    flags: MessageFlags.IsComponentsV2,
                                    components: [v2Container] 
                                });
                                deliverySuccess = true;
                            }
                        } catch (channelError) {
                            ConsoleLogger.error('ReminderScheduler', `Fallback Delivery Failed (Channel: ${channelId}):`, channelError);
                        }
                    }
                }
            }

            // Synchronous delete from DB after attempt (regardless of success, to prevent loop)
            // In a more perfect world, we might want to retry on failure, but for now we delete to avoid spam logic complexity.
            db.deleteReminder(dbId);

        } catch (error) {
            ConsoleLogger.error('ReminderScheduler', `Failed to deliver reminder ${dbId}:`, error);
        }
    }
};
