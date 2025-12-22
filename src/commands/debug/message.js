const { MessageFlags, PermissionFlagsBits } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const { sanitizeInput, validateLength, validateNoForbiddenPatterns, FORBIDDEN_PATTERNS } = require('../.validation');

// Configuration constants
const MAX_MESSAGE_LENGTH = 2000;
const MIN_MESSAGE_LENGTH = 1;

/**
 * Debug Message Handler - Sends messages as the bot
 * Supports both ephemeral and public message sending with security controls
 */

module.exports = {
    /**
     * Handles message sending
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<string|null>} - Returns status message or null on error
     */
    async handle(interaction) {
        const messageText = interaction.options.getString('message');
        const isEphemeral = interaction.options.getBoolean('ephemeral') ?? false;

        // Validate message is not empty
        if (!messageText || messageText.trim().length === 0) {
            return interaction.reply({
                content: 'Message cannot be empty!',
                flags: MessageFlags.Ephemeral
            });
        }

        // Validate length
        if (!validateLength(messageText, MIN_MESSAGE_LENGTH, MAX_MESSAGE_LENGTH)) {
            return interaction.reply({ 
                content: `Message must be between ${MIN_MESSAGE_LENGTH} and ${MAX_MESSAGE_LENGTH} characters.`, 
                flags: MessageFlags.Ephemeral 
            });
        }

        // Sanitize input (prevent control characters but allow basic formatting)
        const sanitized = sanitizeInput(messageText, { allowNewlines: true, allowAnsi: false });

        // Validate doesn't contain forbidden patterns (script tags, etc)
        if (!validateNoForbiddenPatterns(sanitized, [FORBIDDEN_PATTERNS.SCRIPT_TAGS, FORBIDDEN_PATTERNS.URL_SCHEMES])) {
            ConsoleLogger.warn('DebugCommand', `Blocked message with forbidden pattern from ${interaction.user.tag}`);
            return interaction.reply({
                content: '⚠️ Message contains forbidden patterns (script tags, dangerous URLs).',
                flags: MessageFlags.Ephemeral
            });
        }

        // Check for @everyone/@here mentions (require special permission)
        if (/@(everyone|here)/.test(sanitized)) {
            if (!interaction.member?.permissions.has(PermissionFlagsBits.MentionEveryone)) {
                return interaction.reply({
                    content: '❌ You don\'t have permission to use @everyone or @here mentions.',
                    flags: MessageFlags.Ephemeral
                });
            }
        }

        try {
            if (isEphemeral) {
                // Send as ephemeral reply
                await interaction.reply({ 
                    content: sanitized, 
                    flags: MessageFlags.Ephemeral,
                    allowedMentions: { parse: [] } 
                });
            } else {
                // For public messages, validate bot has Send Messages permission
                if (!interaction.channel.permissionsFor(interaction.client.user).has(PermissionFlagsBits.SendMessages)) {
                    return interaction.reply({
                        content: '❌ I don\'t have permission to send messages in this channel.',
                        flags: MessageFlags.Ephemeral
                    });
                }

                // Send as regular message in channel
                await interaction.channel.send({ 
                    content: sanitized, 
                    allowedMentions: { parse: [] } 
                });
                await interaction.reply({ 
                    content: '✅ Message sent!', 
                    flags: MessageFlags.Ephemeral 
                });
            }
            
            ConsoleLogger.info('DebugCommand', `Message sent by ${interaction.user.tag} in ${interaction.channel.name}`);
            return `Channel: ${interaction.channel}\nMessage: ${sanitized}\nEphemeral: ${isEphemeral}`;
        } catch (error) {
            ConsoleLogger.error('DebugCommand', 'Failed to send message:', error);
            
            if (!interaction.replied && !interaction.deferred) {
                await interaction.reply({ 
                    content: '❌ Failed to send message. Permission missing?', 
                    flags: MessageFlags.Ephemeral 
                });
            }
            return null;
        }
    }
};
