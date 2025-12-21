const { MessageFlags } = require('discord.js');
const { updateRoleColor, scheduleNextUpdate } = require('../../daemons/randomRoleColor');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const { getGuildConfig, setGuildConfig } = require('../../utils/db/repo/guildConfig');


/**
 * Debug Role Color Handler - Manually triggers role color updates
 * Forces immediate color refresh for the random role color daemon
 */
module.exports = {
    /**
     * Handles role color subcommands
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @param {import('discord.js').Client} client - Discord client instance
     * @returns {Promise<void>}
     */
    async handle(interaction, client) {
        const subcommand = interaction.options.getSubcommand();
        const guildId = interaction.guildId;

        switch (subcommand) {
            case 'refresh':
                await interaction.deferReply({ flags: MessageFlags.Ephemeral });
                try {
                    await updateRoleColor(client);
                    await interaction.editReply({ content: '🎨 Role color has been refreshed!' });
                } catch (error) {
                    ConsoleLogger.error('Debug', 'Failed to refresh role color:', error);
                    await interaction.editReply({ content: 'Failed to refresh role color.' });
                }
                break;

            case 'set':
                const role = interaction.options.getRole('role');
                
                try {
                    // Get existing config or create new
                    const config = getGuildConfig(guildId) || {};
                    config.randomColorRoleId = role.id;
                    
                    setGuildConfig(guildId, config);
                    
                    ConsoleLogger.info('RandomRoleColor', `Updated target role to ${role.name} (${role.id})`);
                    
                    // Trigger immediate update to show it works
                    updateRoleColor(client).then(() => {
                         // Ensure loop is running
                         scheduleNextUpdate(client);
                    }).catch(() => {});
                    
                    return interaction.reply({ 
                        content: `✅ **Random Color Role Set**\nTarget Role: ${role}\n\nThe color will now update periodically.`, 
                        flags: MessageFlags.Ephemeral 
                    });
                } catch (error) {
                    ConsoleLogger.error('Debug', 'Failed to set random color role:', error);
                    return interaction.reply({ content: '❌ Failed to save configuration.', flags: MessageFlags.Ephemeral });
                }

            case 'reset':
                try {
                    const config = getGuildConfig(guildId);
                    if (config && config.randomColorRoleId) {
                        delete config.randomColorRoleId;
                        setGuildConfig(guildId, config);
                    }
                    
                    ConsoleLogger.info('RandomRoleColor', `Reset configuration.`);
                    
                    return interaction.reply({ 
                        content: `✅ **Configuration Reset**\nReverted to default settings (Environment Variable or Disabled).`, 
                        flags: MessageFlags.Ephemeral 
                    });
                } catch (error) {
                    ConsoleLogger.error('Debug', 'Failed to reset random color role:', error);
                    return interaction.reply({ content: '❌ Failed to reset configuration.', flags: MessageFlags.Ephemeral });
                }

            default:
                ConsoleLogger.warn('RoleColor', `Unknown subcommand: ${subcommand}`);
                await interaction.reply({
                    content: 'Unknown role color command! 🔧',
                    flags: MessageFlags.Ephemeral
                });
        }
    }
};
