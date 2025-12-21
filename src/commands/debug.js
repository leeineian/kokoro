const { SlashCommandBuilder, ChannelType, MessageFlags } = require('discord.js');
const { setPingCategory, runPingCategories, listPingCategories, resetPingCategories } = require('../scripts/webhookPinger');
const { updateRoleColor } = require('../scripts/randomRoleColor');
const ConsoleLogger = require('../utils/consoleLogger');

module.exports = {
    data: new SlashCommandBuilder()
        .setName('debug')
        .setDescription('Debug and Stress Testing Utilities (Admin Only)')
        .setDMPermission(false)
        // --- SUBCOMMAND GROUP: ROLE COLOR ---
        .addSubcommandGroup(group =>
            group
                .setName('rolecolor')
                .setDescription('Manage the random role color script')
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('refresh')
                        .setDescription('Force an immediate color change')))
        // --- SUBCOMMAND GROUP: WEBHOOK PINGER ---
        .addSubcommandGroup(group =>
            group
                .setName('webhook-pinger')
                .setDescription('Manage webhook pinging stress tests')
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('list')
                        .setDescription('List active and configured ping categories'))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('reset')
                        .setDescription('Stop all loops and clear configuration'))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('set')
                        .setDescription('Configure a category for pinging (Does not start)')
                        .addChannelOption(option =>
                            option.setName('category')
                                .setDescription('Select the target Ping Category')
                                .addChannelTypes(ChannelType.GuildCategory)
                                .setRequired(true)))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('run')
                        .setDescription('Start ping loops for all configured categories')
                        .addIntegerOption(option =>
                            option.setName('rounds')
                                .setDescription('Number of ping rounds to execute')
                                .setMinValue(1)
                                .setRequired(true)))),

    async execute(interaction, client) {
        const group = interaction.options.getSubcommandGroup();
        const subcommand = interaction.options.getSubcommand();

        if (!interaction.guild) {
            return interaction.reply({ content: '❌ This command can only be used in servers.', flags: MessageFlags.Ephemeral });
        }

        if (group === 'rolecolor') {
            if (subcommand === 'refresh') {
                await interaction.deferReply({ flags: MessageFlags.Ephemeral });
                try {
                    await updateRoleColor(client);
                    await interaction.editReply({ content: '🎨 Role color has been refreshed!' });
                } catch (error) {
                    ConsoleLogger.error('Debug', 'Failed to refresh role color:', error);
                    await interaction.editReply({ content: 'Failed to refresh role color.' });
                }
            }
            return;
        }

        if (group === 'webhook-pinger') {
            if (subcommand === 'list') {
                await listPingCategories(interaction);
            } else if (subcommand === 'reset') {
                await resetPingCategories(interaction);
            } else if (subcommand === 'set') {
                await setPingCategory(interaction);
            } else if (subcommand === 'run') {
                await runPingCategories(interaction);
            }
        }
    },
};
