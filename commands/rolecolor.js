const { SlashCommandBuilder, PermissionFlagsBits, MessageFlags } = require('discord.js');
const { updateRoleColor } = require('../scripts/randomRoleColor');

module.exports = {
    data: new SlashCommandBuilder()
        .setName('rolecolor')
        .setDescription('Manage the random role color script')
        .setDefaultMemberPermissions(PermissionFlagsBits.Administrator)
        .addSubcommand(subcommand =>
            subcommand
                .setName('refresh')
                .setDescription('Force an immediate color change')),
    async execute(interaction, client) {
        const subcommand = interaction.options.getSubcommand();

        if (subcommand === 'refresh') {
            await interaction.deferReply({ flags: MessageFlags.Ephemeral });
            
            try {
                await updateRoleColor(client);
                await interaction.editReply({ content: '🎨 Role color has been refreshed!' });
            } catch (error) {
                console.error(error);
                await interaction.editReply({ content: 'Failed to refresh role color.' });
            }
        }
    },
};
