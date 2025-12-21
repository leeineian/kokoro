const { SlashCommandBuilder, ChannelType, MessageFlags, PermissionFlagsBits } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const db = require('../../utils/core/database');
const statsHandler = require('./stats');
const logHandler = require('./log');
const roleColorHandler = require('./randomRoleColor');
const messageHandler = require('./message');
const pingHandler = require('./ping');
const webhookLooperHandler = require('./webhookLooper');

/**
 * Debug Command - Main command router for admin debugging and testing utilities
 * Includes system stats, ping tests, logging configuration, and stress testing tools
 */

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
        // --- SUBCOMMAND: PING ---
        .addSubcommand(subcommand =>
            subcommand
                .setName('ping')
                .setDescription('Check bot latency'))
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
                .setName('random-role-color')
                .setDescription('Manage the random role color script')
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('refresh')
                        .setDescription('Force an immediate color change'))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('set')
                        .setDescription('Set the role to randomly color (Overwrites existing)')
                        .addRoleOption(option =>
                            option.setName('role')
                                .setDescription('The role to transform')
                                .setRequired(true)))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('reset')
                        .setDescription('Reset configuration to default')))
        // --- SUBCOMMAND GROUP: MESSAGE ---
        .addSubcommandGroup(group =>
            group
                .setName('message')
                .setDescription('Send messages as the bot')
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('send')
                        .setDescription('Send a message in the current channel')
                        .addStringOption(option =>
                            option.setName('message')
                                .setDescription('The message to send')
                                .setRequired(true))
                        .addBooleanOption(option =>
                            option.setName('ephemeral')
                                .setDescription('Whether the message should be ephemeral (only visible to you)')
                                .setRequired(false))))
        // --- SUBCOMMAND GROUP: WEBHOOK LOOPER ---
        .addSubcommandGroup(group =>
            group
                .setName('webhook-looper')
                .setDescription('Manage webhook looping and channel pinging')
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('list')
                        .setDescription('List and manage loop configurations'))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('set')
                        .setDescription('Configure a channel or category for looping')
                        .addChannelOption(option =>
                            option.setName('channel')
                                .setDescription('Select the target channel or category')
                                .addChannelTypes(ChannelType.GuildText, ChannelType.GuildCategory)
                                .setRequired(true))
                        .addStringOption(option =>
                            option.setName('interval')
                                .setDescription('Time limit (e.g., "5min") or "0" for infinite random mode')
                                .setRequired(false))
                        .addStringOption(option =>
                            option.setName('active_name')
                                .setDescription('Channel/category name while loop is active')
                                .setRequired(false))
                        .addStringOption(option =>
                            option.setName('inactive_name')
                                .setDescription('Channel/category name when loop is inactive')
                                .setRequired(false))
                        .addStringOption(option =>
                            option.setName('message')
                                .setDescription('Message to send in each loop (defaults to @everyone)')
                                .setRequired(false))
                        .addBooleanOption(option =>
                            option.setName('logs')
                                .setDescription('Enable verbose logging in Discord (default: false)')
                                .setRequired(false)))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('start')
                        .setDescription('Start loops for all configured channels')
                        .addBooleanOption(option =>
                            option.setName('logs')
                                .setDescription('Enable verbose logging in Discord (default: false)')
                                .setRequired(false)))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('stop')
                        .setDescription('Stop running loops'))
                .addSubcommand(subcommand =>
                    subcommand
                        .setName('purge')
                        .setDescription('[TEMP] Delete all webhooks in a category')
                        .addChannelOption(option =>
                            option.setName('category')
                                .setDescription('Category to purge webhooks from')
                                .addChannelTypes(ChannelType.GuildCategory)
                                .setRequired(true)))),

    /**
     * Routes the interaction to the appropriate subcommand or group handler
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @param {import('discord.js').Client} client - Discord client instance
     * @returns {Promise<void>}
     */
    async execute(interaction, client) {
        try {
            if (!interaction.guild) {
                return interaction.reply({ content: '❌ This command can only be used in servers.', flags: MessageFlags.Ephemeral });
            }

            const group = interaction.options.getSubcommandGroup();
            const subcommand = interaction.options.getSubcommand();

            // Handle subcommands without groups first
            if (!group) {
                switch (subcommand) {
                    case 'stats':
                        return await statsHandler.handle(interaction, client);
                    case 'ping':
                        return await pingHandler.handle(interaction);
                    case 'log':
                        return await logHandler.handle(interaction);
                    default:
                        ConsoleLogger.warn('DebugCommand', `Unknown subcommand: ${subcommand}`);
                        return interaction.reply({
                            content: 'Unknown debug command! 🔧',
                            flags: MessageFlags.Ephemeral
                        });
                }
            }

            // Handle subcommand groups
            switch (group) {
                case 'random-role-color':
                    return await roleColorHandler.handle(interaction, client);
                case 'message':
                    return await messageHandler.handle(interaction);
                case 'webhook-looper':
                    return await webhookLooperHandler.handle(interaction);
                default:
                    ConsoleLogger.warn('DebugCommand', `Unknown subcommand group: ${group}`);
                    return interaction.reply({
                        content: 'Unknown debug command group! 🔧',
                        flags: MessageFlags.Ephemeral
                    });
            }
        } catch (error) {
            ConsoleLogger.error('DebugCommand', 'Failed to execute debug command:', error);
            
            // Check if we can still reply
            if (!interaction.replied && !interaction.deferred) {
                await interaction.reply({
                    content: 'Something went wrong with the debug command! 🔧',
                    flags: MessageFlags.Ephemeral
                });
            }
        }
    },

    // --- Persistent Handlers ---
    handlers: {
        // Register handlers from sub-modules
        ...pingHandler.handlers,
        ...logHandler.handlers,
        ...statsHandler.handlers,
        ...webhookLooperHandler.handlers
    }
};
