const { MessageFlags } = require('discord.js');
const V2Builder = require('../utils/components');

module.exports = {
    customId: 'ping_refresh',
    async execute(interaction, client) {
        const sent = Date.now();
        const roundtrip = sent - interaction.createdTimestamp;
        const ping = client.ws.ping;

        const v2Container = V2Builder.container([
            V2Builder.section(
                '# Pong!',
                V2Builder.button(`${roundtrip}ms`, 'ping_refresh', roundtrip < 200 ? 3 : 4)
            )
        ]);

        try {
            await interaction.update({ 
                flags: MessageFlags.IsComponentsV2,
                components: [v2Container] 
            });
        } catch (err) {
            console.error('Failed to update ping:', err);
            if (!interaction.replied && !interaction.deferred) {
                await interaction.reply({ content: 'Failed to refresh ping.', flags: MessageFlags.Ephemeral });
            }
        }
    },
};
