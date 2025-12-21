const { MessageFlags } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');

// Configuration constants
const MAX_MESSAGE_LENGTH = 2000;

/**
 * Debug Message Handler - Sends messages as the bot
 * Supports both ephemeral and public message sending
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

        if (messageText.length > MAX_MESSAGE_LENGTH) {
            return interaction.reply({ 
                content: `Message is too long (limit is ${MAX_MESSAGE_LENGTH} characters).`, 
                flags: MessageFlags.Ephemeral 
            });
        }

        try {
            if (isEphemeral) {
                // Send as ephemeral reply
                await interaction.reply({ 
                    content: messageText, 
                    flags: MessageFlags.Ephemeral,
                    allowedMentions: { parse: [] } 
                });
            } else {
                // Send as regular message in channel
                await interaction.channel.send({ 
                    content: messageText, 
                    allowedMentions: { parse: [] } 
                });
                await interaction.reply({ 
                    content: '✅ Message sent!', 
                    flags: MessageFlags.Ephemeral 
                });
            }
            return `Channel: ${interaction.channel}\nMessage: ${messageText}\nEphemeral: ${isEphemeral}`;
        } catch (error) {
            ConsoleLogger.error('DebugCommand', 'Failed to send message:', error);
            await interaction.reply({ 
                content: '❌ Failed to send message. Permission missing?', 
                flags: MessageFlags.Ephemeral 
            });
            return null;
        }
    }
};
