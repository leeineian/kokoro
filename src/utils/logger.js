const { MessageFlags } = require('discord.js');
const chalk = require('chalk');
const V2Builder = require('./components');
const db = require('./database');

// In-memory cache
let loggingConfig = {};

// Load on startup
try {
    loggingConfig = db.getAllGuildConfigs();
    console.log(chalk.green(`[Logger] Loaded configs for ${Object.keys(loggingConfig).length} guilds.`));
} catch (err) {
    console.error(chalk.red('Failed to load logging config from DB:'), err);
}

function saveGuildConfig(guildId) {
    if (loggingConfig[guildId]) {
        db.setGuildConfig(guildId, loggingConfig[guildId]);
    }
}

function getLoggingConfig() {
    return loggingConfig;
}

// Ensure init/get helper
function getGuildConfig(guildId) {
    if (!loggingConfig[guildId]) {
        loggingConfig[guildId] = { enabled: false, channelId: null };
    }
    return loggingConfig[guildId];
}

async function logAction(client, guildId, user, action, descriptions) {
    if (!guildId) return;
    
    const config = loggingConfig[guildId];
    if (!config || !config.enabled || !config.channelId) return;

    const channelId = config.channelId;
    const channel = client.channels.cache.get(channelId);
    
    if (!channel) return;

    // Components V2 Implementation
    const v2Container = V2Builder.container([
        V2Builder.section(
            `**${action}**\nUser: ${user.tag}\n\n${descriptions}`, 
            V2Builder.thumbnail(user.displayAvatarURL())
        ),
        V2Builder.actionRow([
            V2Builder.button('Dismiss', 'dismiss_log', 4) // Style 4 = Red/Danger
        ])
    ]);

    try {
        await channel.send({ 
            flags: MessageFlags.IsComponentsV2,
            components: [v2Container] 
        });
    } catch (error) {
        console.error(chalk.red('Failed to send log:'), JSON.stringify(error, null, 2));
    }
}

module.exports = {
    getLoggingConfig,
    getGuildConfig,
    saveGuildConfig,
    logAction
};
