const GUILD_ID = '832245857595555861';
const ROLE_ID = '1450798622294675627';
const MIN_MINUTES = 1;
const MAX_MINUTES = 10;

function getRandomColor() {
    return Math.floor(Math.random() * 16777216); // Random integer for hex color (0 to 0xFFFFFF)
}

async function updateRoleColor(client) {
    try {
        const guild = await client.guilds.fetch(GUILD_ID);
        if (!guild) {
            console.warn(`[RandomColor] Guild ${GUILD_ID} not found/cached.`);
            return;
        }

        const role = await guild.roles.fetch(ROLE_ID);
        if (!role) {
            console.warn(`[RandomColor] Role ${ROLE_ID} not found in guild.`);
            return;
        }

        const newColor = getRandomColor();
        await role.edit({ colors: { primaryColor: newColor } });
        
        console.log(`[RandomColor] Updated role color to #${newColor.toString(16).padStart(6, '0')}`);

    } catch (error) {
        console.error('[RandomColor] Failed to update role color:', error);
    }
}

function scheduleNextUpdate(client) {
    // Random minute between MIN and MAX (inclusive)
    const minutes = Math.floor(Math.random() * (MAX_MINUTES - MIN_MINUTES + 1)) + MIN_MINUTES;
    const ms = minutes * 60 * 1000;
    
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
    updateRoleColor
};
