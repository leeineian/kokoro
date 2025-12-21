const { MessageFlags } = require('discord.js');
const V2Builder = require('../../utils/core/components');
const ConsoleLogger = require('../../utils/log/consoleLogger');

// Configuration constants
const MAX_RETRY_ATTEMPTS = 3;
const API_TIMEOUT_MS = 5000;
const RETRY_DELAY_MS = 500;

/**
 * Cat Image Command - Fetches random cat images from TheCatAPI
 * Includes retry logic with timeout handling for reliability
 */
module.exports = {
    /**
     * Executes the cat image command
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<string|void>} - Returns status message or void on error
     */
    async execute(interaction) {
        await interaction.deferReply();

        try {
            let catUrl = null;
            let attempts = 0;

            while (!catUrl && attempts < MAX_RETRY_ATTEMPTS) {
                attempts++;
                try {
                    const controller = new AbortController();
                    const timeoutId = setTimeout(() => controller.abort(), API_TIMEOUT_MS);

                    const response = await fetch('https://api.thecatapi.com/v1/images/search', {
                        signal: controller.signal
                    });
                    clearTimeout(timeoutId);
                    if (!response.ok) throw new Error(`API status: ${response.status}`);
                    
                    const data = await response.json();
                    catUrl = data[0]?.url;
                } catch (e) {
                    if (attempts === MAX_RETRY_ATTEMPTS) throw e;
                    await new Promise(r => setTimeout(r, RETRY_DELAY_MS));
                }
            }

            if (!catUrl) {
                throw new Error(`No cat found after ${MAX_RETRY_ATTEMPTS} attempts`);
            }

            const v2Container = V2Builder.container([
                V2Builder.mediaGallery([
                    { media: { url: catUrl } }
                ])
            ]);

            await interaction.editReply({ 
                flags: MessageFlags.IsComponentsV2,
                components: [v2Container] 
            });

            return 'Requested a cat image';
        } catch (error) {
            ConsoleLogger.error('CatCommand', 'Failed to fetch cat image:', error);
            await interaction.editReply({ 
                content: 'Failed to fetch a cat image! 😿', 
                flags: MessageFlags.Ephemeral 
            });
            return;
        }
    }
};
