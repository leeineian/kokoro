const { MessageFlags } = require('discord.js');
const { logAction, getGuildConfig, saveGuildConfig } = require('../../utils/auditLogger');

module.exports = {
    async handle(interaction) {
        const guildId = interaction.guildId;
        const config = getGuildConfig(guildId);

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
        
        saveGuildConfig(guildId);
        
        if (config.channelId && config.enabled) {
             // Try a test log
             logAction(guildId, 'Debug', `Audit Logging Configured by ${interaction.user.tag}`);
        }

        if (response) {
            await interaction.reply({ 
                content: response, 
                allowedMentions: { parse: [] },
                flags: MessageFlags.Ephemeral
            });
        } else {
            await interaction.reply({ 
                content: 'No valid options provided. Usage: `/debug log channel: #channel` or `/debug log toggle: true/false`', 
                flags: MessageFlags.Ephemeral 
            });
        }
    }
};
