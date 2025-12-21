const { SlashCommandBuilder, MessageFlags } = require('discord.js');

module.exports = {
	data: new SlashCommandBuilder()
		.setName('ping')
		.setDescription('Replies with bot latency.'),
	async execute(interaction, client) {
        await interaction.deferReply();
        const sent = await interaction.fetchReply();
        const roundtrip = sent.createdTimestamp - interaction.createdTimestamp;

        const V2Builder = require('../../utils/components');

        const v2Container = V2Builder.container([
            V2Builder.section(
                '# Pong!',
                V2Builder.button(`${roundtrip}ms`, 'ping_refresh', roundtrip < 200 ? 3 : 4)
            )
        ]);

        await interaction.editReply({ 
            components: [v2Container],
            flags: MessageFlags.IsComponentsV2
        });

        return `Latency check: ${roundtrip}ms`;
	},
    handlers: {
        'ping_refresh': async (interaction) => {
            const MessageFlags = require('discord.js').MessageFlags;
            const V2Builder = require('../../utils/components');
            
            const sent = interaction.message.createdTimestamp; // Use message timestamp
            // Note: This isn't perfect for recalculating roundtrip on edit but sufficient for refresh feel
            const freshRoundtrip = Date.now() - sent; 
            // Better: just measures DB/System latency simulation if we had it. 
            // For now, let's just create a new pseudo-latency or just re-render.
            
            // To properly measure API latency again we'd need to send a fresh message.
            // For this simple example we'll just re-calculate against current time vs start.
            
            const newRoundtrip = Date.now() - interaction.createdTimestamp;

            const v2Container = V2Builder.container([
                V2Builder.section(
                    '# 🔁 Pong!',
                    V2Builder.button(`${newRoundtrip}ms`, 'ping_refresh', newRoundtrip < 200 ? 3 : 4)
                )
            ]);

            await interaction.update({
                components: [v2Container],
                flags: MessageFlags.IsComponentsV2
            });
        }
    }
};
