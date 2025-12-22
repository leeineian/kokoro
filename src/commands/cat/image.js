const { MessageFlags } = require('discord.js');
const V2Builder = require('../../utils/core/components');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const { checkRateLimit, validateUrl } = require('../.validation');
const { cacheImage, getCachedImage } = require('./.cache');
const { handleCommandError, retryOperation } = require('../.errorHandler');

// Configuration constants
const RATE_LIMIT = { maxRequests: 15, windowMs: 60000 }; // 15 requests per minute
const MAX_RETRY_ATTEMPTS = 3;
const API_TIMEOUT_MS = 5000;
const RETRY_DELAY_MS = 500;
const ALLOWED_DOMAINS = ['*.thecatapi.com', 'cdn2.thecatapi.com'];

/**
 * Cat Image Command - Fetches random cat images from TheCatAPI
 * Includes retry logic with exponential backoff and timeout handling for reliability
 */
module.exports = {
    /**
     * Executes the cat image command
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<string|void>} - Returns status message or void on error
     */
    async execute(interaction) {
        // Rate limiting check
        if (!checkRateLimit(interaction.user.id, 'cat_image', RATE_LIMIT.maxRequests, RATE_LIMIT.windowMs)) {
            return interaction.reply({
                content: '⏱️ Slow down! Too many cat images requested. Please wait a moment.',
                flags: MessageFlags.Ephemeral
            });
        }

        await interaction.deferReply();

        try {
            // Try cache first (30% of the time to reduce API load)
            if (Math.random() < 0.3) {
                const cachedUrl = getCachedImage();
                if (cachedUrl) {
                    const v2Container = V2Builder.container([
                        V2Builder.mediaGallery([
                            { media: { url: cachedUrl } }
                        ])
                    ]);
                    await interaction.editReply({ 
                        flags: MessageFlags.IsComponentsV2,
                        components: [v2Container] 
                    });
                    return 'Served cached cat image';
                }
            }

            let catUrl = null;
            let attempts = 0;

            while (!catUrl && attempts < MAX_RETRY_ATTEMPTS) {
                attempts++;
                try {
                    const controller = new AbortController();
                    const timeoutId = setTimeout(() => controller.abort(), API_TIMEOUT_MS);

                    const response = await fetch('https://api.thecatapi.com/v1/images/search', {
                        signal: controller.signal,
                        headers: { 'User-Agent': 'MinderBot/1.0' }
                    });
                    clearTimeout(timeoutId);
                    
                    if (!response.ok) {
                        throw new Error(`API status: ${response.status}`);
                    }
                    
                    const data = await response.json();
                    
                    // Validate response structure
                    if (!Array.isArray(data) || data.length === 0 || !data[0]?.url) {
                        throw new Error('Invalid response structure');
                    }
                    
                    const url = data[0].url;
                    
                    // Validate URL is from expected domain
                    if (!validateUrl(url, ALLOWED_DOMAINS)) {
                        ConsoleLogger.warn('CatCommand', `Suspicious URL received: ${url}`);
                        throw new Error('Invalid URL domain');
                    }
                    
                    catUrl = url;
                } catch (e) {
                    if (attempts === MAX_RETRY_ATTEMPTS) {
                        throw e;
                    }
                    // Exponential backoff
                    const delay = RETRY_DELAY_MS * Math.pow(2, attempts - 1);
                    await new Promise(r => setTimeout(r, delay));
                }
            }

            if (!catUrl) {
                throw new Error(`No cat found after ${MAX_RETRY_ATTEMPTS} attempts`);
            }

            // Cache the image URL for future use
            cacheImage(catUrl);

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
            // Try to serve from cache as fallback
            const cachedUrl = getCachedImage();
            if (cachedUrl) {
                const v2Container = V2Builder.container([
                    V2Builder.mediaGallery([
                        { media: { url: cachedUrl } }
                    ])
                ]);
                await interaction.editReply({ 
                    flags: MessageFlags.IsComponentsV2,
                    components: [v2Container] 
                });
                return 'Served cached cat image (API failed)';
            }
            
            // Use centralized error handler
            await handleCommandError(interaction, error, 'CatImage', {
                customMessage: 'Failed to fetch a cat image! The service may be temporarily unavailable. 😿'
            });
            return;
        }
    }
};
