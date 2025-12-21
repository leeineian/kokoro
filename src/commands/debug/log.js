const { MessageFlags, PermissionFlagsBits } = require('discord.js');
const { logAction, getGuildConfig, saveGuildConfig } = require('../../utils/log/auditLogger');
const ConsoleLogger = require('../../utils/log/consoleLogger');

/**
 * Debug Log Handler - Configures audit logging for guilds
 * Manages logging channel and enabled/disabled status
 */

module.exports = {
    /**
     * Handles log configuration
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<void>}
     */
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
    },
    
    handlers: {
        'dismiss_log': async (interaction) => {
            try {
                if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
                    return interaction.reply({ content: 'Only admins can dismiss logs.', flags: MessageFlags.Ephemeral });
                }
                await interaction.message.delete();
            } catch (err) {
                ConsoleLogger.error('DebugCommand', 'Failed to delete log:', err);
                await interaction.reply({ content: 'Failed to delete log.', flags: MessageFlags.Ephemeral });
            }
        }
    }
};
