const { MessageFlags } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const db = require('../../utils/core/database');
const { DEFAULT_SYSTEM_PROMPT, MAX_PROMPT_LENGTH } = require('./.helper');


/**
 * AI Prompt Handler - Manages user-specific custom AI system prompts
 */
module.exports = {
    /**
     * Handles AI prompt subcommands (set, reset, view)
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<void>}
     */
    async handle(interaction) {
        try {
            const subcommand = interaction.options.getSubcommand();
            const userId = interaction.user.id;

            switch (subcommand) {
                case 'set': {
                    const text = interaction.options.getString('text');
                    
                    if (text.length > MAX_PROMPT_LENGTH) {
                        return interaction.reply({ 
                            content: `❌ Prompt is too long! Keep it under ${MAX_PROMPT_LENGTH} characters.`, 
                            flags: MessageFlags.Ephemeral 
                        });
                    }
                    
                    db.ai.setPrompt(userId, text);
                    return interaction.reply({ 
                        content: `✅ **Custom Prompt Set!**\n> *${text}*`, 
                        flags: MessageFlags.Ephemeral 
                    });
                }

                case 'reset':
                    db.ai.deletePrompt(userId);
                    return interaction.reply({ 
                        content: `🔄 **Prompt Reset.**\n> Default: *${DEFAULT_SYSTEM_PROMPT}*`, 
                        flags: MessageFlags.Ephemeral 
                    });

                case 'view': {
                    const prompt = db.ai.getPrompt(userId) || DEFAULT_SYSTEM_PROMPT;
                    return interaction.reply({ 
                        content: `**Your Prompt:**\n> ${prompt}`, 
                        flags: MessageFlags.Ephemeral 
                    });
                }

                default:
                    ConsoleLogger.warn('AIPrompt', `Unknown subcommand: ${subcommand}`);
                    return interaction.reply({
                        content: 'Unknown prompt command! 🤖',
                        flags: MessageFlags.Ephemeral
                    });
            }
        } catch (error) {
            ConsoleLogger.error('AIPrompt', 'Failed to handle prompt command:', error);
            
            if (!interaction.replied && !interaction.deferred) {
                await interaction.reply({
                    content: 'Failed to process prompt command! 🤖',
                    flags: MessageFlags.Ephemeral
                });
            }
        }
    }
};
