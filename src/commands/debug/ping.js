const { MessageFlags } = require('discord.js');
const V2Builder = require('../../utils/core/components');

/**
 * Debug Ping Handler - Tests bot latency with interactive refresh
 * Measures roundtrip time for Discord API requests
 */

module.exports = {
    /**
     * Handles ping command with interactive refresh button
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<string>} - Returns latency status message
     */
    async handle(interaction) {
        await interaction.deferReply();
        const sent = await interaction.fetchReply();
        const roundtrip = sent.createdTimestamp - interaction.createdTimestamp;

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
            const newRoundtrip = Date.now() - interaction.createdTimestamp;

            const v2Container = V2Builder.container([
                V2Builder.section(
                    '# 🔁 Pong!',
                    V2Builder.button(`${newRoundtrip}ms`, 'ping_refresh', newRoundtrip < 200 ? 3 : 4)
                )
            ]);

            await interaction.update({
                content: null,
                components: [v2Container],
                flags: MessageFlags.IsComponentsV2
            });
        }
    }
};

