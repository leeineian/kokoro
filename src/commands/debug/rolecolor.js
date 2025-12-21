const { MessageFlags } = require('discord.js');
const { updateRoleColor } = require('../../scripts/randomRoleColor');
const ConsoleLogger = require('../../utils/consoleLogger');

module.exports = {
    async handle(interaction, client) {
        const subcommand = interaction.options.getSubcommand();
        
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
    }
};
