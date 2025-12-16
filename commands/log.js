const { SlashCommandBuilder, PermissionFlagsBits, ChannelType, MessageFlags } = require('discord.js');
const { getLoggingConfig, saveLoggingConfig } = require('../utils/logger');

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

        const config = getLoggingConfig();
        const guildId = interaction.guildId;
        
        if (!config[guildId]) {
            config[guildId] = { enabled: false, channelId: null };
        }

        const channel = interaction.options.getChannel('channel');
        const toggle = interaction.options.getBoolean('toggle');

        let response = '';

        if (channel) {
            config[guildId].channelId = channel.id;
            response += `Logging channel set to ${channel}.\n`;
        }

        if (toggle !== null) {
            config[guildId].enabled = toggle;
            response += `Logging ${toggle ? 'enabled' : 'disabled'}.\n`;
        }
        
        await saveLoggingConfig();
	},
};
