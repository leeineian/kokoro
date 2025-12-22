const os = require('os');
const { MessageFlags } = require('discord.js');
const V2Builder = require('../../utils/core/components');
const db = require('../../utils/core/database');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const { title, key, val } = require('./.helper');

// Cache configuration
const CACHE_TTL_MS = 5000; // 5 second cache
let statsCache = {
    system: { data: null, timestamp: 0 },
    metrics: { data: null, timestamp: 0, interactionId: null }
};

// --- Data Gathering ---

/**
 * Gathers system hardware and OS statistics (cached)
 * @returns {string} - Formatted ANSI string with system stats
 */
const getSystemStats = () => {
    const now = Date.now();
    
    // Check cache
    if (statsCache.system.data && (now - statsCache.system.timestamp) < CACHE_TTL_MS) {
        return statsCache.system.data;
    }
    
    // Compute fresh stats
    const usedMem = (process.memoryUsage().heapUsed / 1024 / 1024).toFixed(2);
    const totalMem = (os.totalmem() / 1024 / 1024 / 1024).toFixed(2);
    const data = [
        title('System'),
        `${key('Platform')} ${val(process.platform)}`,
        `${key('Operating System')} ${val(os.type() + ' ' + os.release())}`,
        `${key('Memory')} ${val(`${usedMem} MB / ${totalMem} GB`)}`,
        `${key('CPU')} ${val(os.cpus()[0].model)}`,
        `${key('PID')} ${val(process.pid)}`
    ].join('\n');
    
    // Update cache
    statsCache.system = { data, timestamp: now };
    return data;
};

/**
 * Gathers application runtime statistics
 * @param {import('discord.js').Client} client - Discord client instance
 * @param {Object} [healthMetrics={}] - Optional health metrics (ping, dbLatency)
 * @returns {string} - Formatted ANSI string with app stats
 */
const getAppStats = (client, healthMetrics = {}) => {
    // UPTIME
    const uptimeSeconds = Math.floor(process.uptime());
    const days = Math.floor(uptimeSeconds / 86400);
    const hours = Math.floor((uptimeSeconds % 86400) / 3600);
    const minutes = Math.floor((uptimeSeconds % 3600) / 60);
    const uptimeStr = `${days}d ${hours}h ${minutes}m`;
    const totalUsers = client.guilds.cache.reduce((acc, guild) => acc + guild.memberCount, 0);

    const lines = [
        title('App'),
        `${key('Versions')} ${val(`Bun ${Bun.version} / DJS ${require('discord.js').version}`)}`,
        `${key('Uptime')} ${val(uptimeStr)}`,
        `${key('Servers')} ${val(client.guilds.cache.size)}`,
        `${key('Users')} ${val(totalUsers)}`
    ];

    if (healthMetrics.ping) {
        lines.push(`${key('Ping')} ${val(healthMetrics.ping + 'ms')}`);
    }
    if (healthMetrics.dbLatency) {
        lines.push(`${key('Database')} ${val(healthMetrics.dbLatency + 'ms')}`);
    }

    return lines.join('\n');
};

/**
 * Get cached or fresh metrics for an interaction
 * @param {string} interactionId - Interaction ID
 * @param {import('discord.js').Client} client - Discord client  
 * @param {string} userId - User ID for DB query
 * @param {boolean} includePing - Whether to include ping measurement
 * @returns {Promise<Object>} - Metrics object
 */
async function getMetrics(interactionId, client, userId, includePing = false) {
    const now = Date.now();
    
    // Check if we have fresh cached metrics for this interaction
    if (statsCache.metrics.interactionId === interactionId &&
        (now - statsCache.metrics.timestamp) < CACHE_TTL_MS) {
        return statsCache.metrics.data;
    }
    
    // Calculate fresh metrics
    const metrics = {};
    
    // DB latency
    const dbStart = performance.now();
    try {
        db.getRemindersCount(userId);
    } catch (e) {
        ConsoleLogger.error('Debug', 'DB Count failed:', e);
    }
    metrics.dbLatency = (performance.now() - dbStart).toFixed(2);
    
    // Optionally include ping (only on initial load, not on filter changes)
    if (includePing) {
        // This should be set by caller who has access to the sent message
        // We'll leave it undefined here and let caller add it
    }
    
    // Cache metrics
    statsCache.metrics = {
        data: metrics,
        timestamp: now,
        interactionId: interactionId
    };
    
    return metrics;
}

/**
 * Renders stats UI with interactive filter
 * @param {string} selection - Filter selection ('all', 'system', 'app')
 * @param {import('discord.js').Client} client - Discord client instance  
 * @param {Object} [healthMetrics={}] - Optional health metrics
 * @returns {Object} - V2 container with stats and filter menu
 */
const renderStats = (selection, client, healthMetrics = {}) => {
    let output = '';
    if (selection === 'system') {
        output = getSystemStats();
    } else if (selection === 'app') {
        output = getAppStats(client, healthMetrics);
    } else {
        // All
        output = getSystemStats() + '\n\n' + getAppStats(client, healthMetrics);
    }
    
    const v2Container = V2Builder.container([
        V2Builder.textDisplay(`\`\`\`ansi\n${output}\n\`\`\``),
        V2Builder.actionRow([
            V2Builder.selectMenu('debug_stats_filter', [
                { label: 'All', value: 'all', description: 'Show all statistics', default: selection === 'all' },
                { label: 'System', value: 'system', description: 'Show system hardware stats', default: selection === 'system' },
                { label: 'App', value: 'app', description: 'Show application stats', default: selection === 'app' }
            ], 'Filter Statistics')
        ])
    ]);
    return v2Container;
};

module.exports = {
    renderStats,
    /**
     * Handles stats command execution
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @param {import('discord.js').Client} client - Discord client instance
     * @returns {Promise<void>}
     */
    async handle(interaction, client) {
         try {
             // Defer reply immediately (no flags needed for defer)
             await interaction.deferReply();
             
             // Calculate Metrics (with ping)
             const sent = await interaction.fetchReply();
             const roundTrip = sent.createdTimestamp - interaction.createdTimestamp;
             
             const metrics = await getMetrics(interaction.id, client, interaction.user.id, true);
             metrics.ping = roundTrip; // Add ping to cached metrics

             await interaction.editReply({
                 components: [renderStats('all', client, metrics)],
                 flags: MessageFlags.IsComponentsV2
             });
         } catch (error) {
             ConsoleLogger.error('Debug', 'Stats command failed:', error);
             // Only try to reply if interaction is still valid
             if (interaction.deferred || interaction.replied) {
                 try {
                     await interaction.editReply({ content: '❌ Failed to fetch statistics.' });
                 } catch (e) {
                     ConsoleLogger.error('Debug', 'Failed to send error message:', e);
                 }
             } else {
                 try {
                     await interaction.reply({ content: '❌ Failed to fetch statistics.', flags: MessageFlags.Ephemeral });
                 } catch (e) {
                     ConsoleLogger.error('Debug', 'Failed to send error message:', e);
                 }
             }
         }
    },
    
    handlers: {
        'debug_stats_filter': async (interaction) => {
            const selection = interaction.values[0];
            
            // Use cached metrics if available (TTL: 5 seconds)
            const metrics = await getMetrics(interaction.message.interaction.id, interaction.client, interaction.user.id, false);

            await interaction.update({
                content: null,
                components: [renderStats(selection, interaction.client, metrics)],
                flags: MessageFlags.IsComponentsV2
            });
        }
    }
};
