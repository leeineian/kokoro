const { MessageFlags } = require('discord.js');

module.exports = {
    customId: 'dismiss_message',
    async execute(interaction) {
        try {
            await interaction.deferUpdate(); 

            let message = interaction.message;

            if (!message.channel) {
                const channel = await interaction.client.channels.fetch(interaction.channelId);
                message = await channel.messages.fetch(interaction.message.id);
            }

            await message.delete();
        } catch (err) {
            console.error('Failed to dismiss message:', err);
            if (err.code !== 10008) {
                await interaction.followUp({ 
                    content: 'Failed to delete message.', 
                    flags: MessageFlags.Ephemeral 
                });
            }
        }
    },
};
