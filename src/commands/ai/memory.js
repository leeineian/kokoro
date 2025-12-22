const { MessageFlags, PermissionFlagsBits } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const db = require('../../utils/core/database');
const { checkRateLimit } = require('../.validation');
const { RATE_LIMITS } = require('./.helper');
const { logAction } = require('../../utils/log/auditLogger');

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
                    
                    // Rate limiting check
                    const { maxRequests, windowMs } = RATE_LIMITS.MEMORY_RESET;
                    if (!checkRateLimit(interaction.user.id, 'ai_memory_reset', maxRequests, windowMs)) {
                        return interaction.reply({
                            content: '⏱️ Slow down! You\'re resetting memory too frequently. Please wait a moment.',
                            flags: MessageFlags.Ephemeral
                        });
                    }
                    
                    // Validate guild context (prevent cross-guild manipulation)
                    if (!interaction.guildId) {
                        return interaction.reply({
                            content: '❌ This command can only be used in a server.',
                            flags: MessageFlags.Ephemeral
                        });
                    }
                    
                    const channelId = interaction.channelId;
                    
                    // Validate channel ownership (ensure user has access to this channel)
                    const channel = interaction.channel;
                    if (!channel) {
                        return interaction.reply({
                            content: '❌ Unable to access this channel.',
                            flags: MessageFlags.Ephemeral
                        });
                    }
                    
                    db.ai.resetMemory(channelId);
                    
                    // Audit logging
                    logAction(
                        interaction.client,
                        interaction.guildId,
                        interaction.user,
                        'AI Memory Reset',
                        `Channel: <#${channelId}>`
                    );
                    
                    ConsoleLogger.info('AIMemory', `Memory reset for channel ${channelId} by ${interaction.user.tag}`);
                    
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
