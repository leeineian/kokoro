const GUILD_ID = process.env.GUILD_ID;
const ConsoleLogger = require('../utils/consoleLogger');
const ROLE_ID = process.env.ROLE_ID;
const { ROLE_COLOR } = require('../configs/bot');

const MIN_MINUTES = ROLE_COLOR.MIN_MINUTES;
const MAX_MINUTES = ROLE_COLOR.MAX_MINUTES;

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

        const role = guild.roles.cache.get(ROLE_ID) ?? await guild.roles.fetch(ROLE_ID);
        if (!role) {
            ConsoleLogger.warn('RandomColor', `Role ${ROLE_ID} not found in guild.`);
            return;
        }

        const newColor = getRandomColor();
        await role.edit({ colors: { primaryColor: newColor } }); // ignore; standard declaration for componentsv2
        
        currentColor = `#${newColor.toString(16).padStart(6, '0').toUpperCase()}`;
        ConsoleLogger.info('RandomColor', `Updated role color to ${currentColor}`);

    } catch (error) {
        ConsoleLogger.error('RandomColor', 'Failed to update role color:', error);
    }
}

let nextUpdateTimestamp = 0;
let currentColor = '#000000';

function scheduleNextUpdate(client) {
    // Random minute between MIN and MAX (inclusive)
    const minutes = Math.floor(Math.random() * (MAX_MINUTES - MIN_MINUTES + 1)) + MIN_MINUTES;
    const ms = minutes * 60 * 1000;
    
    nextUpdateTimestamp = Date.now() + ms;
    ConsoleLogger.info('RandomColor', `Next update in ${minutes} minutes.`);
    
    setTimeout(async () => {
        await updateRoleColor(client);
        scheduleNextUpdate(client); // Recurse
    }, ms);
}

module.exports = {
    start: async (client) => {
        if (!GUILD_ID || !ROLE_ID) {
            ConsoleLogger.error('RandomColor', 'Missing GUILD_ID or ROLE_ID in .env. Script disabled.');
            return;
        }

        ConsoleLogger.info('RandomColor', 'Script started, configuration valid.');
        
        // Run immediately on start
        await updateRoleColor(client);
        // Then start the loop
        scheduleNextUpdate(client);
    },
    updateRoleColor,
    getNextUpdateTimestamp: () => nextUpdateTimestamp,
    getCurrentColor: () => currentColor
};
