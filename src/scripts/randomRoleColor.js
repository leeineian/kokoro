const GUILD_ID = '832245857595555861';
const ROLE_ID = '1450798622294675627';
const MIN_MINUTES = 1;
const MAX_MINUTES = 10;

function getRandomColor() {
    return Math.floor(Math.random() * 16777216); // Random integer for hex color (0 to 0xFFFFFF)
}

async function updateRoleColor(client) {
    try {
        const guild = client.guilds.cache.get(GUILD_ID) ?? await client.guilds.fetch(GUILD_ID);
        if (!guild) {
            console.warn(`[RandomColor] Guild ${GUILD_ID} not found/cached.`);
            return;
        }

        const role = guild.roles.cache.get(ROLE_ID) ?? await guild.roles.fetch(ROLE_ID);
        if (!role) {
            console.warn(`[RandomColor] Role ${ROLE_ID} not found in guild.`);
            return;
        }

        const newColor = getRandomColor();
        await role.edit({ colors: { primaryColor: newColor } });
        
        currentColor = `#${newColor.toString(16).padStart(6, '0').toUpperCase()}`;
        console.log(`[RandomColor] Updated role color to ${currentColor}`);

    } catch (error) {
        console.error('[RandomColor] Failed to update role color:', error);
    }
}

let nextUpdateTimestamp = 0;
let currentColor = '#000000';

function scheduleNextUpdate(client) {
    // Random minute between MIN and MAX (inclusive)
    const minutes = Math.floor(Math.random() * (MAX_MINUTES - MIN_MINUTES + 1)) + MIN_MINUTES;
    const ms = minutes * 60 * 1000;
    
    nextUpdateTimestamp = Date.now() + ms;
    console.log(`[RandomColor] Next update in ${minutes} minutes.`);
    
    setTimeout(async () => {
        await updateRoleColor(client);
        scheduleNextUpdate(client); // Recurse
    }, ms);
}

module.exports = {
    start: async (client) => {
        console.log('[RandomColor] Script started.');
        // Run immediately on start
        await updateRoleColor(client);
        // Then start the loop
        scheduleNextUpdate(client);
    },
    updateRoleColor,
    getNextUpdateTimestamp: () => nextUpdateTimestamp,
    getCurrentColor: () => currentColor
};
