const { SlashCommandBuilder, MessageFlags } = require('discord.js');
const V2Builder = require('../utils/components');

module.exports = {
	data: new SlashCommandBuilder()
		.setName('cat')
		.setDescription('Serves a random cat in a box')
        .addStringOption(option =>
            option.setName('type')
                .setDescription('What do you want?')
                .addChoices(
                    { name: 'Image (default)', value: 'image' },
                    { name: 'Fact', value: 'fact' }
                )),
	async execute(interaction) {
        await interaction.deferReply();
        const type = interaction.options.getString('type') || 'image';

        try {
            if (type === 'fact') {
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
            }

            // Default: Image
            let catUrl = null;
            let attempts = 0;

            while (!catUrl && attempts < 3) {
                attempts++;
                try {
                    const controller = new AbortController();
                    const timeoutId = setTimeout(() => controller.abort(), 5000); // 5s timeout

                    const response = await fetch('https://api.thecatapi.com/v1/images/search', {
                        signal: controller.signal
                    });
                    clearTimeout(timeoutId);
                    if (!response.ok) throw new Error(`API status: ${response.status}`);
                    
                    const data = await response.json();
                    catUrl = data[0]?.url;
                } catch (e) {
                    if (attempts === 3) throw e;
                    await new Promise(r => setTimeout(r, 500)); // Delay before retry
                }
            }

            if (!catUrl) {
                throw new Error('No cat found after 3 attempts');
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
            console.error('Cat Command Error:', error);
            await interaction.editReply({ 
                content: `Failed to fetch a cat ${type}! 😿`, 
                flags: MessageFlags.Ephemeral 
            });
        }
	},
};
