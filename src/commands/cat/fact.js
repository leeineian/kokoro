const { MessageFlags } = require('discord.js');
const V2Builder = require('../../utils/core/components');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const { checkRateLimit, validateUrl } = require('../.validation');
const { cacheFact, getCachedFact } = require('./.cache');
const { handleCommandError, retryOperation } = require('../.errorHandler');

// Rate limiting and circuit breaker config
const RATE_LIMIT = { maxRequests: 10, windowMs: 60000 }; // 10 requests per minute
const CIRCUIT_BREAKER = { failureThreshold: 3, resetTime: 60000 };
const API_TIMEOUT_MS = 5000;
const ALLOWED_DOMAINS = ['catfact.ninja'];

// Circuit breaker state
let failureCount = 0;
let circuitOpen = false;
let circuitResetTimer = null;

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
        // Rate limiting check
        if (!checkRateLimit(interaction.user.id, 'cat_fact', RATE_LIMIT.maxRequests, RATE_LIMIT.windowMs)) {
            return interaction.reply({
                content: '⏱️ Slow down! Too many cat facts requested. Please wait a moment.',
                flags: MessageFlags.Ephemeral
            });
        }

        await interaction.deferReply();

        try {
            // Check circuit breaker
            if (circuitOpen) {
                ConsoleLogger.warn('CatCommand', 'Circuit breaker open for cat fact API');
                // Try to serve from cache
                const cachedFact = getCachedFact();
                if (cachedFact) {
                    const v2Container = V2Builder.container([
                        V2Builder.textDisplay(`${cachedFact}\n\n*⚠️ Served from cache (API temporarily unavailable)*`)
                    ]);
                    await interaction.editReply({ 
                        flags: MessageFlags.IsComponentsV2,
                        components: [v2Container] 
                    });
                    return 'Served cached cat fact (circuit breaker active)';
                }
                
                await interaction.editReply({ 
                    content: 'The cat fact API is temporarily unavailable. Please try again in a moment! 😿',
                    flags: MessageFlags.Ephemeral 
                });
                return;
            }

            // Try cache first (20% of the time to reduce API load)
            if (Math.random() < 0.2) {
                const cachedFact = getCachedFact();
                if (cachedFact) {
                    const v2Container = V2Builder.container([
                        V2Builder.textDisplay(cachedFact)
                    ]);
                    await interaction.editReply({ 
                        flags: MessageFlags.IsComponentsV2,
                        components: [v2Container] 
                    });
                    return 'Served cached cat fact';
                }
            }

            // Fetch from API with timeout
            const controller = new AbortController();
            const timeoutId = setTimeout(() => controller.abort(), API_TIMEOUT_MS);
            
            const response = await fetch('https://catfact.ninja/fact', {
                headers: { 'User-Agent': 'MinderBot/1.0 (https://github.com/minder)' },
                signal: controller.signal
            });
            clearTimeout(timeoutId);
            
            if (!response.ok) {
                throw new Error(`API status: ${response.status}`);
            }
            
            const data = await response.json();
            
            // Validate response structure
            if (!data || typeof data.fact !== 'string' || data.fact.trim().length === 0) {
                throw new Error('Invalid response structure');
            }

            const fact = data.fact.trim();
            
            // Cache the fact for future use
            cacheFact(fact);
            
            // Reset circuit breaker on success
            failureCount = 0;

            const v2Container = V2Builder.container([
                V2Builder.textDisplay(fact)
            ]);

            await interaction.editReply({ 
                flags: MessageFlags.IsComponentsV2,
                components: [v2Container] 
            });
            
            return 'Requested a cat fact';
        } catch (error) {
            // Increment failure count for circuit breaker
            failureCount++;
            if (failureCount >= CIRCUIT_BREAKER.failureThreshold) {
                circuitOpen = true;
                ConsoleLogger.warn('CatCommand', `Circuit breaker opened after ${failureCount} failures`);
                
                // Schedule circuit reset
                if (circuitResetTimer) clearTimeout(circuitResetTimer);
                circuitResetTimer = setTimeout(() => {
                    circuitOpen = false;
                    failureCount = 0;
                    ConsoleLogger.info('CatCommand', 'Circuit breaker reset');
                }, CIRCUIT_BREAKER.resetTime);
            }
            
            // Try to serve from cache as fallback
            const cachedFact = getCachedFact();
            if (cachedFact) {
                const v2Container = V2Builder.container([
                    V2Builder.textDisplay(`${cachedFact}\n\n*⚠️ Served from cache (API unavailable)*`)
                ]);
                await interaction.editReply({ 
                    flags: MessageFlags.IsComponentsV2,
                    components: [v2Container] 
                });
                return 'Served cached cat fact (API failed)';
            }
            
            // Use centralized error handler for user-friendly message
            await handleCommandError(interaction, error, 'CatFact', {
                customMessage: 'Failed to fetch a cat fact! The service may be temporarily unavailable. 😿'
            });
            return;
        }
    }
};
