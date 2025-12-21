const { SlashCommandBuilder } = require('discord.js');
const promptHandler = require('./prompt');
const memoryHandler = require('./memory');

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
    
    async execute(interaction) {
        const group = interaction.options.getSubcommandGroup();

        if (group === 'prompt') {
            await promptHandler.handle(interaction);
        } else if (group === 'memory') {
            await memoryHandler.handle(interaction);
        }
    },
};
