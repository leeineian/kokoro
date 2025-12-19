const { SlashCommandBuilder, MessageFlags } = require('discord.js');

module.exports = {
	data: new SlashCommandBuilder()
		.setName('ping')
		.setDescription('Replies with bot latency.'),
	async execute(interaction, client) {
        const sent = Date.now();
        const roundtrip = sent - interaction.createdTimestamp;
        const ping = client.ws.ping;
        
const V2Builder = require('../utils/components');

// ...
        
        // Components V2 Container
        const v2Container = V2Builder.container([
            V2Builder.section(
                '# Pong!',
                V2Builder.button(`${roundtrip}ms`, 'ping_refresh', roundtrip < 200 ? 3 : 4)
            )
        ]);

        await interaction.reply({ 
            flags: MessageFlags.IsComponentsV2,
            components: [v2Container] 
        });

        return `Latency check: ${ping}ms`;
	},
};
