const { MessageFlags } = require('discord.js');
const V2Builder = require('../../utils/core/components');
const ConsoleLogger = require('../../utils/log/consoleLogger');

/**
 * Cat Fact Command - Fetches random cat facts from catfact.ninja API
 */
module.exports = {
    /**
     * Executes the cat fact command
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<string|void>} - Returns status message or void on error
     */
    async execute(interaction) {
        await interaction.deferReply();

        try {
            const response = await fetch('https://catfact.ninja/fact', {
                headers: { 'User-Agent': 'MinderBot/1.0 (https://github.com/minder)' }
            });
            
            if (!response.ok) throw new Error(`API status: ${response.status}`);
            
            const data = await response.json();
            if (!data.fact) throw new Error('No fact received');

            const v2Container = V2Builder.container([
                V2Builder.textDisplay(data.fact)
            ]);

            await interaction.editReply({ 
                flags: MessageFlags.IsComponentsV2,
                components: [v2Container] 
            });
            return 'Requested a cat fact';
        } catch (error) {
            ConsoleLogger.error('CatCommand', 'Failed to fetch cat fact:', error);
            await interaction.editReply({ 
                content: 'Failed to fetch a cat fact! 😿', 
                flags: MessageFlags.Ephemeral 
            });
            return;
        }
    }
};
