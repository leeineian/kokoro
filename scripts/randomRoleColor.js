const GUILD_ID = '832245857595555861';
const ROLE_ID = '1450798622294675627';
const INTERVAL_MS = 10 * 60 * 1000; // 10 Minutes

function getRandomColor() {
    return Math.floor(Math.random() * 16777215); // Random integer for hex color
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
        await role.edit({ color: newColor });
        
        console.log(`[RandomColor] Updated role color to #${newColor.toString(16).padStart(6, '0')}`);

    } catch (error) {
        console.error('[RandomColor] Failed to update role color:', error);
    }
}

module.exports = {
    start: (client) => {
        console.log('[RandomColor] Script started.');
        
        // Initial run after a short delay
        setTimeout(() => updateRoleColor(client), 5000);

        // Interval loop
        setInterval(() => {
            updateRoleColor(client);
        }, INTERVAL_MS);
    },
    updateRoleColor
};
