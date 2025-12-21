const { SlashCommandBuilder, ChannelType, MessageFlags, PermissionFlagsBits } = require('discord.js');
const db = require('../../utils/database');
const statsHandler = require('./stats');
const logHandler = require('./log');
const roleColorHandler = require('./rolecolor');
const webhookPingerHandler = require('./webhookPinger');

module.exports = {
    data: new SlashCommandBuilder()
        .setName('debug')
        .setDescription('Debug and Stress Testing Utilities (Admin Only)')
        .setDMPermission(false)
        .setDefaultMemberPermissions(PermissionFlagsBits.Administrator)
        // --- SUBCOMMAND: STATS ---
        .addSubcommand(subcommand =>
            subcommand
                .setName('stats') 
                .setDescription('Display detailed system and application statistics'))
        // --- SUBCOMMAND: LOG ---
        .addSubcommand(subcommand =>
            subcommand
                .setName('log')
                .setDescription('Configure audit logging')
                .addChannelOption(option => 
                    option.setName('channel')
                        .setDescription('Channel to send logs to')
                        .addChannelTypes(ChannelType.GuildText)
                )
                .addBooleanOption(option =>
                    option.setName('toggle')
                        .setDescription('Enable or disable logging')
                ))
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

        if (subcommand === 'stats') {
             await statsHandler.handle(interaction, client);
             return;
        }

        if (subcommand === 'log') {
            await logHandler.handle(interaction);
            return;
        }

        if (group === 'rolecolor') {
            await roleColorHandler.handle(interaction, client);
            return;
        }

        if (group === 'webhook-pinger') {
            await webhookPingerHandler.handle(interaction);
        }
    },

    // --- Persistent Handlers ---
    handlers: {
        'dismiss_log': async (interaction) => {
             const { PermissionFlagsBits, MessageFlags } = require('discord.js');
             try {
                if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
                    return interaction.reply({ content: 'Only admins can dismiss logs.', flags: MessageFlags.Ephemeral });
                }
                await interaction.message.delete();
            } catch (err) {
                console.error('Failed to delete log:', err);
                await interaction.reply({ content: 'Failed to delete log.', flags: MessageFlags.Ephemeral });
            }
        },
        'debug_stats_filter': async (interaction, client) => {
            const selection = interaction.values[0];
            
            const dbStart = performance.now();
            db.getRemindersCount(interaction.user.id);
            const dbLatency = (performance.now() - dbStart).toFixed(2);
            
            const metrics = { ping: 'Refreshed', dbLatency };

            await interaction.update({
                components: [statsHandler.renderStats(selection, client, metrics)],
                flags: MessageFlags.IsComponentsV2
            });
        }
    }
};
