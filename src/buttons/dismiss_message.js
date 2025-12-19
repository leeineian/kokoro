const { MessageFlags } = require('discord.js');

module.exports = {
    customId: 'dismiss_message',
    async execute(interaction) {
        try {
            await interaction.deferUpdate(); 

            let message = interaction.message;

            if (!message.channel) {
                // If fetching fails (ephemeral), ignore error and try deleting via interaction
                try {
                    const channel = await interaction.client.channels.fetch(interaction.channelId);
                    message = await channel.messages.fetch(interaction.message.id);
                } catch (e) {
                    // Ignore fetch error, likely ephemeral
                }
            }

            try {
                await message.delete();
            } catch (deleteErr) {
                // If delete fails, it might be ephemeral or we lack permissions.
                // Try deleting the interaction answer (works for ephemeral components)
                await interaction.deleteReply();
            }
        } catch (err) {
            console.error('Failed to dismiss message:', err);
            // Only follow up if it's NOT a delete error we just handled
            if (err.code !== 10008 && err.code !== 50035 && err.code !== 'Unknown Message') { 
                 // Consider suppressing or just logging
            }
        }
    },
};
