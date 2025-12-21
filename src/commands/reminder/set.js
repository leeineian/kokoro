const { MessageFlags } = require('discord.js');
const chrono = require('chrono-node');
const V2Builder = require('../../utils/core/components');
const db = require('../../utils/core/database');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const reminderScheduler = require('../../daemons/reminderScheduler');

/**
 * Reminder Set Handler - Creates and schedules new reminders
 * Handles natural language time parsing, DM/channel delivery, and confirmation messages
 */
module.exports = {
    /**
     * Handles reminder creation
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<void>}
     */
    async handle(interaction) {
        const message = interaction.options.getString('message');
        const when = interaction.options.getString('when');
        const sendTo = interaction.options.getString('sendto') || 'dm';

        const parsedDate = chrono.parseDate(when);
        if (!parsedDate) {
            return interaction.reply({ 
                content: 'I could not understand that time. Please try again (e.g. "in 10 minutes", "tomorrow at 5pm").', 
                flags: MessageFlags.Ephemeral 
            });
        }
        if (parsedDate <= new Date()) {
            return interaction.reply({ 
                content: 'That time is in the past! Please choose a future time.', 
                flags: MessageFlags.Ephemeral 
            });
        }
        const dueAt = parsedDate.getTime();

        // Defer immediately to allow time for DB and DM operations
        await interaction.deferReply({ flags: MessageFlags.Ephemeral });

        try {
            // Persist to DB
            const id = db.addReminder(interaction.user.id, interaction.channelId, message, dueAt, sendTo);

            // Schedule Delivery
            reminderScheduler.scheduleReminder(interaction.client, interaction.user.id, interaction.channelId, message, id, sendTo, dueAt);

            let dmUrl = null;
            let dmFailed = false;

            // DM Confirmation Logic
            if (sendTo === 'dm') {
                const v2Container = V2Builder.container([
                    V2Builder.textDisplay(
                        `📅 **Reminder Set**\nI'll remind you <t:${Math.floor(dueAt / 1000)}:R>.\nMessage: "${message}"`
                    ),
                    V2Builder.actionRow([
                        V2Builder.button('Dismiss', 'dismiss_message', 4)
                    ])
                ]);
                
                try {
                    const sentMsg = await interaction.user.send({ 
                        content: null,
                        components: [v2Container],
                        flags: MessageFlags.IsComponentsV2
                    });
                    dmUrl = sentMsg.url;
                } catch (err) {
                    ConsoleLogger.error('Reminder', `DM Confirmation Failed (User: ${interaction.user.id}):`, err);
                    dmFailed = true;
                }
            }

            if (dmFailed) {
                 await interaction.editReply({ 
                    content: `⚠️ I could not send you a DM (Privacy Settings). Here is your confirmation:\n\n📅 **Reminder Set**\nI'll remind you <t:${Math.floor(dueAt / 1000)}:R>.\nMessage: "${message}"`
                });
            } else {
                const locationText = sendTo === 'channel' 
                    ? `<#${interaction.channelId}>` 
                    : (dmUrl ? `[your DMs](${dmUrl})` : 'your DMs');

                await interaction.editReply({
                    content: `Reminder set! I'll ping you in ${locationText}.`
                });
            }

        } catch (error) {
            ConsoleLogger.error('Reminder', 'Failed to set reminder:', error);
            await interaction.editReply({ 
                content: 'Failed to save reminder.'
            });
        }
    }
};
