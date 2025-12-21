const { MessageFlags, PermissionFlagsBits } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const db = require('../../utils/core/database');

/**
 * AI Memory Handler - Manages AI conversation memory/context per channel
 */
module.exports = {
    /**
     * Handles AI memory subcommands (reset)
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<void>}
     */
    async handle(interaction) {
        try {
            const subcommand = interaction.options.getSubcommand();
            
            switch (subcommand) {
                case 'reset':
                    // Check for administrator permissions
                    if (!interaction.member?.permissions.has(PermissionFlagsBits.Administrator)) {
                        return interaction.reply({
                            content: '❌ Only administrators can reset AI memory.',
                            flags: MessageFlags.Ephemeral
                        });
                    }
                    
                    const channelId = interaction.channelId;
                    db.ai.resetMemory(channelId);
                    
                    // This is NOT ephemeral, so everyone knows the context was wiped
                    return interaction.reply({ 
                        content: `🧠 **AI Memory Wiped.**\nI have forgotten everything said in this channel before this moment.` 
                    });

                default:
                    ConsoleLogger.warn('AIMemory', `Unknown subcommand: ${subcommand}`);
                    return interaction.reply({
                        content: 'Unknown memory command! 🤖',
                        flags: MessageFlags.Ephemeral
                    });
            }
        } catch (error) {
            ConsoleLogger.error('AIMemory', 'Failed to handle memory command:', error);
            
            if (!interaction.replied && !interaction.deferred) {
                await interaction.reply({
                    content: 'Failed to process memory command! 🤖',
                    flags: MessageFlags.Ephemeral
                });
            }
        }
    }
};
