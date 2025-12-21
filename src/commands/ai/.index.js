const { SlashCommandBuilder, MessageFlags } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const promptHandler = require('./prompt');
const memoryHandler = require('./memory');

/**
 * AI Command - Main command router for AI-related subcommand groups
 * Manages custom AI personality prompts and conversation memory
 */
module.exports = {
    data: new SlashCommandBuilder()
        .setName('ai')
        .setDescription('Manage AI settings')
        // Subcommand Group: PROMPT
        .addSubcommandGroup(group =>
            group
                .setName('prompt')
                .setDescription('Manage your custom AI personality')
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('set')
                        .setDescription('Set a custom system prompt')
                        .addStringOption(option =>
                            option.setName('text')
                                .setDescription('The system prompt (e.g., "You are a pirate")')
                                .setRequired(true)))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('reset')
                        .setDescription('Reset your prompt to default'))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('view')
                        .setDescription('View your current custom prompt')))
        // Subcommand Group: MEMORY
        .addSubcommandGroup(group =>
            group
                .setName('memory')
                .setDescription('Manage AI context memory')
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('reset')
                        .setDescription('Clears the AI memory for this channel (starts fresh)'))),
    
    /**
     * Routes the interaction to the appropriate subcommand group handler
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<void>} - Returns void after routing to handler
     */
    async execute(interaction) {
        try {
            const group = interaction.options.getSubcommandGroup();

            switch (group) {
                case 'prompt':
                    return await promptHandler.handle(interaction);
                case 'memory':
                    return await memoryHandler.handle(interaction);
                default:
                    ConsoleLogger.warn('AICommand', `Unknown subcommand group: ${group}`);
                    await interaction.reply({
                        content: 'Unknown AI command! 🤖',
                        flags: MessageFlags.Ephemeral
                    });
                    return;
            }
        } catch (error) {
            ConsoleLogger.error('AICommand', 'Failed to execute AI command:', error);
            
            // Check if we can still reply
            if (!interaction.replied && !interaction.deferred) {
                await interaction.reply({
                    content: 'Something went wrong with the AI command! 🤖',
                    flags: MessageFlags.Ephemeral
                });
            }
        }
    },
};
