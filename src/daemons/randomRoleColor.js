const GUILD_ID = process.env.GUILD_ID;
const ConsoleLogger = require('../utils/log/consoleLogger');
const { getGuildConfig } = require('../utils/db/repo/guildConfig');

const MIN_MINUTES = 1;
const MAX_MINUTES = 10;

function getRandomColor() {
    return Math.floor(Math.random() * 16777216); // Random integer for hex color (0 to 0xFFFFFF)
}

async function updateRoleColor(client) {
    try {
        const guild = client.guilds.cache.get(GUILD_ID) ?? await client.guilds.fetch(GUILD_ID);
        if (!guild) {
            ConsoleLogger.warn('RandomColor', `Guild ${GUILD_ID} not found/cached.`);
            return;
        }

        const config = getGuildConfig(GUILD_ID);
        const roleId = config?.randomColorRoleId;

        if (!roleId) {
            return false; // Silent return to stop logic
        }

        const role = guild.roles.cache.get(roleId) ?? await guild.roles.fetch(roleId);
        if (!role) {
            ConsoleLogger.warn('RandomColor', `Role ${roleId} not found in guild.`);
            return;
        }

        const newColor = getRandomColor();
        await role.edit({ colors: { primaryColor: newColor } }); // ignore; standard declaration for componentsv2
        
        currentColor = `#${newColor.toString(16).padStart(6, '0').toUpperCase()}`;
        ConsoleLogger.info('RandomColor', `Updated role color to ${currentColor}`);
        return true;

    } catch (error) {
        ConsoleLogger.error('RandomColor', 'Failed to update role color:', error);
        return false;
    }
}

let nextUpdateTimestamp = 0;
let currentColor = '#000000';
let nextTimeout = null;

function scheduleNextUpdate(client) {
    // Random minute between MIN and MAX (inclusive)
    const minutes = Math.floor(Math.random() * (MAX_MINUTES - MIN_MINUTES + 1)) + MIN_MINUTES;
    const ms = minutes * 60 * 1000;
    
    nextUpdateTimestamp = Date.now() + ms;
    ConsoleLogger.info('RandomColor', `Next update in ${minutes} minutes.`);
    
    // Clear any existing timeout
    if (nextTimeout) clearTimeout(nextTimeout);
    
    nextTimeout = setTimeout(async () => {
        const success = await updateRoleColor(client);
        // Only reschedule if successful (meaning role exists)
        if (success) {
            scheduleNextUpdate(client); // Recurse
        } else {
            ConsoleLogger.warn('RandomColor', 'Update failed or no role set. Stopping loop until manual restart via /debug set.');
        }
    }, ms);
}

module.exports = {
    start: async (client) => {
        if (!GUILD_ID) {
            ConsoleLogger.error('RandomColor', 'Missing GUILD_ID in .env. Script disabled.');
            return;
        }

        const config = getGuildConfig(GUILD_ID);
        const roleId = config?.randomColorRoleId;

        if (!roleId) {
            ConsoleLogger.warn('RandomColor', 'No role ID configured. Use /debug random-role-color set <role> to configure.');
            // Do NOT start loop if no role
            return;
        }

        ConsoleLogger.info('RandomColor', 'Script started, configuration valid.');
        
        // Run immediately on start
        await updateRoleColor(client);
        // Then start the loop
        scheduleNextUpdate(client);
    },
    
    stop: () => {
        if (nextTimeout) {
            clearTimeout(nextTimeout);
            nextTimeout = null;
            ConsoleLogger.info('RandomColor', 'Script stopped.');
        }
    },
    
    updateRoleColor,
    scheduleNextUpdate, // Exported to allow restarting from command
    getNextUpdateTimestamp: () => nextUpdateTimestamp,
    getCurrentColor: () => currentColor
};
