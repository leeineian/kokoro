const fs = require('fs');
const path = require('path');
const { MessageFlags } = require('discord.js');
const V2Builder = require('./components');

const LOGGING_FILE = path.join(__dirname, '../logging.json');

// Helper to load/save logging config
let loggingConfig = {};
try {
    if (fs.existsSync(LOGGING_FILE)) {
        loggingConfig = JSON.parse(fs.readFileSync(LOGGING_FILE, 'utf8'));
    }
} catch (err) {
    console.error('Failed to load logging config:', err);
}

async function saveLoggingConfig() {
    try {
        await fs.promises.writeFile(LOGGING_FILE, JSON.stringify(loggingConfig, null, 2));
    } catch (err) {
        console.error('Failed to save logging config:', err);
    }
}

function getLoggingConfig() {
    return loggingConfig;
}

async function logAction(client, guildId, user, action, descriptions) {
    if (!guildId || !loggingConfig[guildId] || !loggingConfig[guildId].enabled || !loggingConfig[guildId].channelId) return;

    const channelId = loggingConfig[guildId].channelId;
    const channel = client.channels.cache.get(channelId);
    
    if (!channel) return;



    // Components V2 Implementation
    const v2Container = V2Builder.container([
        V2Builder.section(
            `**${action}**\nUser: ${user.tag}\n\n${descriptions}`, 
            V2Builder.thumbnail(user.displayAvatarURL())
        ),
        V2Builder.actionRow([
            V2Builder.button('Dismiss', 'dismiss_log', 2)
        ])
    ]);

    try {
        await channel.send({ 
            flags: MessageFlags.IsComponentsV2,
            components: [v2Container] 
        });
    } catch (error) {
        console.error('Failed to send log:', JSON.stringify(error, null, 2));
    }
}

module.exports = {
    getLoggingConfig,
    saveLoggingConfig,
    logAction
};
