const { MessageFlags, PermissionFlagsBits } = require('discord.js');
const chrono = require('chrono-node');
const V2Builder = require('../../utils/core/components');
const db = require('../../utils/core/database');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const reminderScheduler = require('../../daemons/reminderScheduler');
const { validateFutureTimestamp, validateMinimumInterval, checkRateLimit, sanitizeInput, validateLength } = require('../.validation');

// Security constants
const MAX_MESSAGE_LENGTH = 500;
const MIN_MESSAGE_LENGTH = 1;
const MAX_REMINDERS_PER_USER = 100;
const MIN_INTERVAL_MS = 60000; // 1 minute minimum
const MAX_FUTURE_MS = 31536000000; // 1 year maximum
const RATE_LIMIT = { maxRequests: 10, windowMs: 60000 }; // 10 reminders per minute

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

        // Rate limiting check
        if (!checkRateLimit(interaction.user.id, 'reminder_set', RATE_LIMIT.maxRequests, RATE_LIMIT.windowMs)) {
            return interaction.reply({
                content: '⏱️ Slow down! You\'re creating reminders too frequently. Please wait a moment.',
                flags: MessageFlags.Ephemeral
            });
        }

        // Validate message
        if (!message || message.trim().length === 0) {
            return interaction.reply({
                content: 'Message cannot be empty!',
                flags: MessageFlags.Ephemeral
            });
        }

        if (!validateLength(message, MIN_MESSAGE_LENGTH, MAX_MESSAGE_LENGTH)) {
            return interaction.reply({ 
                content: `Message must be between ${MIN_MESSAGE_LENGTH} and ${MAX_MESSAGE_LENGTH} characters.`, 
                flags: MessageFlags.Ephemeral 
            });
        }

        // Sanitize message
        const sanitizedMessage = sanitizeInput(message, { allowNewlines: true, allowAnsi: false });
        
        if (sanitizedMessage.trim().length === 0) {
            return interaction.reply({
                content: '⚠️ Your message contains only invalid characters!',
                flags: MessageFlags.Ephemeral
            });
        }

        // Check user's reminder count
        const userReminders = db.getReminders(interaction.user.id);
        if (userReminders.length >= MAX_REMINDERS_PER_USER) {
            return interaction.reply({
                content: `⚠️ You've reached the maximum of ${MAX_REMINDERS_PER_USER} active reminders. Please delete some before creating new ones.`,
                flags: MessageFlags.Ephemeral
            });
        }

        // Parse time
        const parsedDate = chrono.parseDate(when);
        if (!parsedDate) {
            return interaction.reply({ 
                content: 'I could not understand that time. Please try again (e.g. "in 10 minutes", "tomorrow at 5pm").', 
                flags: MessageFlags.Ephemeral 
            });
        }

        const dueAt = parsedDate.getTime();

        // Validate timestamp is in the future
        if (dueAt <= Date.now()) {
            return interaction.reply({ 
                content: 'That time is in the past! Please choose a future time.', 
                flags: MessageFlags.Ephemeral 
            });
        }

        // Validate timestamp is not too soon (spam prevention)
        if (!validateMinimumInterval(dueAt, MIN_INTERVAL_MS)) {
            return interaction.reply({
                content: `⏱️ Reminder time must be at least ${MIN_INTERVAL_MS / 1000} seconds in the future.`,
                flags: MessageFlags.Ephemeral
            });
        }

        // Validate timestamp is not too far in the future
        if (!validateFutureTimestamp(dueAt, MAX_FUTURE_MS)) {
            return interaction.reply({
                content: `⏱️ Reminder time cannot be more than 1 year in the future.`,
                flags: MessageFlags.Ephemeral
            });
        }

        // If channel delivery, validate permissions
        if (sendTo === 'channel') {
            const channel = interaction.channel;
            
            // Check bot has permission to send messages in this channel
            if (!channel.permissionsFor(interaction.client.user)?.has(PermissionFlagsBits.SendMessages)) {
                return interaction.reply({
                    content: '❌ I don\'t have permission to send messages in this channel. Choose DM delivery instead.',
                    flags: MessageFlags.Ephemeral
                });
            }
            
            // Check user has permission to view channel (prevent abuse)
            if (!channel.permissionsFor(interaction.member)?.has(PermissionFlagsBits.ViewChannel)) {
                return interaction.reply({
                    content: '❌ You don\'t have permission to set reminders in this channel.',
                    flags: MessageFlags.Ephemeral
                });
            }
        }

        // Defer immediately to allow time for DB and DM operations
        await interaction.deferReply({ flags: MessageFlags.Ephemeral });

        try {
            // Persist to DB
            const id = db.addReminder(interaction.user.id, interaction.channelId, sanitizedMessage, dueAt, sendTo);

            // Schedule Delivery
            reminderScheduler.scheduleReminder(interaction.client, interaction.user.id, interaction.channelId, sanitizedMessage, id, sendTo, dueAt);

            let dmUrl = null;
            let dmFailed = false;

            // DM Confirmation Logic
            if (sendTo === 'dm') {
                const v2Container = V2Builder.container([
                    V2Builder.textDisplay(
                        `📅 **Reminder Set**\nI'll remind you <t:${Math.floor(dueAt / 1000)}:R>.\nMessage: "${sanitizedMessage}"`
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

            ConsoleLogger.info('Reminder', `Reminder created by ${interaction.user.tag} for <t:${Math.floor(dueAt / 1000)}:R>`);

            if (dmFailed) {
                 await interaction.editReply({ 
                    content: `⚠️ I could not send you a DM (Privacy Settings). Here is your confirmation:\n\n📅 **Reminder Set**\nI'll remind you <t:${Math.floor(dueAt / 1000)}:R>.\nMessage: "${sanitizedMessage}"`
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
