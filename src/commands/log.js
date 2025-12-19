const { SlashCommandBuilder, PermissionFlagsBits, ChannelType, MessageFlags } = require('discord.js');
const {  getGuildConfig, saveGuildConfig } = require('../utils/logger');

module.exports = {
    data: new SlashCommandBuilder()
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
        ),
    async execute(interaction) {
        if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
            return interaction.reply({ content: 'You do not have permission to use this command.', flags: MessageFlags.Ephemeral });
        }

        const guildId = interaction.guildId;
        const config = getGuildConfig(guildId); // Use helper

        const channel = interaction.options.getChannel('channel');
        const toggle = interaction.options.getBoolean('toggle');

        let response = '';

        if (channel) {
            config.channelId = channel.id;
            response += `Logging channel set to ${channel}.\n`;
        }

        if (toggle !== null) {
            config.enabled = toggle;
            response += `Logging ${toggle ? 'enabled' : 'disabled'}.\n`;
        }
        
        // Save for this guild only
        saveGuildConfig(guildId);

        if (response) {
            await interaction.reply({ 
                content: response, 
                allowedMentions: { parse: [] } 
            });
        } else {
            await interaction.reply({ 
                content: 'No valid options provided. Usage: `/log channel: #channel` or `/log toggle: true/false`', 
                flags: MessageFlags.Ephemeral 
            });
        }
    },
};
