const { ActivityType } = require('discord.js');
const db = require('../utils/database');
const { getNextUpdateTimestamp, getCurrentColor } = require('./randomRoleColor');

const ROTATION_INTERVAL_MS = 60000; // 60 Seconds (Safe for rate limits)
const IDLE_THRESHOLD = 1 * 60 * 1000; // 1 Minute
const START_TIME = Date.now();

const statusGenerators = [
    // 1. Pending Reminders
    async () => {
        try {
            const count = await db.getRemindersCount();
            return { 
                name: 'Custom Status', 
                type: ActivityType.Custom, 
                state: `Serving ${count} pending reminders` 
            };
        } catch (e) {
            console.error('Failed to fetching reminder count for status:', e);
            return null;
        }
    },
    
    // 2. Color Update
    async () => {
        const nextUpdate = getNextUpdateTimestamp();
        const currentColor = getCurrentColor();
        if (!nextUpdate) return null;
        
        const diffMinutes = Math.ceil((nextUpdate - Date.now()) / 60000);
        
        const timeStr = diffMinutes === 1 ? '1 minute' : `${diffMinutes} minutes`;
        return {
            name: 'Custom Status',
            type: ActivityType.Custom,
            state: `${currentColor} in ${timeStr}`
        };
    },

    // 3. Uptime
    async () => {
        const uptimeMs = Date.now() - START_TIME;
        const hours = Math.floor(uptimeMs / 3600000);
        const minutes = Math.floor((uptimeMs % 3600000) / 60000);
        
        return {
            name: 'Custom Status', 
            type: ActivityType.Custom, 
            state: `Uptime: ${hours}h ${minutes}m`
        };
    },

    // 4. Latency
    async (client) => {
        const ping = Math.round(client.ws.ping);
        if (ping < 0) return null; // Invalid/Not ready yet
        return {
            name: 'Custom Status',
            type: ActivityType.Custom,
            state: `Ping: ${ping}ms`
        };
    },

    // 5. UTC Time
    async () => {
        const now = new Date();
        const timeStr = now.toISOString().split('T')[1].substring(0, 5); // HH:MM
        return {
            name: 'Custom Status',
            type: ActivityType.Custom,
            state: `Time: ${timeStr} UTC`
        };
    }
];

async function updateStatus(client) {
    // Pick a random status
    let index = Math.floor(Math.random() * statusGenerators.length);
    let generator = statusGenerators[index];
    
    let presenceData = await generator(client);

    // Fallback if the chosen one failed (returned null)
    if (!presenceData) {
        // Try Uptime (Index 2) as a safe fallback
        presenceData = await statusGenerators[2](client);
    }

    if (presenceData) {
        // Dynamic Status Logic
        const timeSinceLastActivity = Date.now() - lastActivityTime;
        const status = timeSinceLastActivity < IDLE_THRESHOLD ? 'online' : 'dnd';

        client.user.setPresence({
            activities: [presenceData],
            status: status,
        });
    } else {
        // Absolute fallback if everything fails, just force DND
        client.user.setPresence({ status: 'dnd' });
    }
}

let lastActivityTime = Date.now();

module.exports = {
    start: (client) => {
        console.log('[StatusRotator] Script started.');
        
        // Initial Update
        updateStatus(client);

        // Interval
        setInterval(() => {
            updateStatus(client);
        }, ROTATION_INTERVAL_MS);
    },
    recordActivity: (client) => {
        const timeSinceLastActivity = Date.now() - lastActivityTime;
        const wasIdle = timeSinceLastActivity >= IDLE_THRESHOLD;

        lastActivityTime = Date.now();

        if (wasIdle) {
            console.log('[StatusRotator] Waking up from IDLE, forcing status update.');
            updateStatus(client);
        }
    }
};
