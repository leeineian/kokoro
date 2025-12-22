const { MessageFlags } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const db = require('../../utils/core/database');
const { DEFAULT_SYSTEM_PROMPT, MAX_PROMPT_LENGTH, RATE_LIMITS, sanitizePrompt, validatePromptSafety } = require('./.helper');
const { checkRateLimit, validateLength, validateEncoding } = require('../.validation');
const { handleCommandError } = require('../.errorHandler');


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
                    
                    // Validate not empty
                    if (!text || text.trim().length === 0) {
                        return interaction.reply({ 
                            content: '❌ Prompt cannot be empty!', 
                            flags: MessageFlags.Ephemeral 
                        });
                    }
                    
                    // Validate length
                    if (!validateLength(text, 1, MAX_PROMPT_LENGTH)) {
                        return interaction.reply({ 
                            content: `❌ Prompt is too long! Keep it under ${MAX_PROMPT_LENGTH} characters.`, 
                            flags: MessageFlags.Ephemeral 
                        });
                    }
                    
                    // Validate UTF-8 encoding
                    if (!validateEncoding(text)) {
                        return interaction.reply({ 
                            content: '❌ Prompt contains invalid characters!', 
                            flags: MessageFlags.Ephemeral 
                        });
                    }
                    
                    // Sanitize input
                    const sanitized = sanitizePrompt(text);
                    
                    // Validate doesn't contain prompt injection attempts
                    if (!validatePromptSafety(sanitized)) {
                        ConsoleLogger.warn('AIPrompt', `Potential injection attempt by ${interaction.user.tag}: ${text.substring(0, 100)}`);
                        return interaction.reply({ 
                            content: '❌ Your prompt contains forbidden patterns that could interfere with AI functionality. Please rephrase.', 
                            flags: MessageFlags.Ephemeral 
                        });
                    }
                    
                    // Rate limiting check
                    const { maxRequests, windowMs } = RATE_LIMITS.PROMPT_UPDATE;
                    if (!checkRateLimit(userId, 'ai_prompt_update', maxRequests, windowMs)) {
                        return interaction.reply({
                            content: '⏱️ You\'re updating your prompt too frequently. Please wait a moment.',
                            flags: MessageFlags.Ephemeral
                        });
                    }
                    
                    db.ai.setPrompt(userId, sanitized);
                    
                    ConsoleLogger.info('AIPrompt', `Prompt updated by ${interaction.user.tag}`);
                    
                    return interaction.reply({ 
                        content: `✅ **Custom Prompt Set!**\n> *${sanitized}*`, 
                        flags: MessageFlags.Ephemeral 
                    });
                }

                case 'reset':
                    db.ai.deletePrompt(userId);
                    ConsoleLogger.info('AIPrompt', `Prompt reset by ${interaction.user.tag}`);
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
            // Use centralized error handler
            await handleCommandError(interaction, error, 'AIPrompt', {
                customMessage: 'Failed to process prompt command! 🤖'
            });
        }
    }
};
