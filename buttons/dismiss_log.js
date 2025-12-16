const { PermissionFlagsBits, MessageFlags } = require('discord.js');

module.exports = {
    customId: 'dismiss_log',
    async execute(interaction) {
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
};
