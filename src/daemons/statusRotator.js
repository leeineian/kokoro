const { ActivityType } = require('discord.js');
const ConsoleLogger = require('../utils/log/consoleLogger');
const db = require('../utils/core/database');
const { getNextUpdateTimestamp, getCurrentColor } = require('./randomRoleColor');

const ROTATION_INTERVAL = 60000; // 60 seconds
const IDLE_THRESHOLD = 60000; // 60 seconds

const START_TIME = Date.now();

// Named Generators for Organization
const generators = {
    reminders: async () => {
        try {
            const count = await db.getRemindersCount();
            if (count === 0) return null;
            return { 
                name: 'Custom Status', 
                type: ActivityType.Custom, 
                state: `${count} pending reminder${count === 1 ? '' : 's'}` 
            };
        } catch (e) {
            ConsoleLogger.error('StatusRotator', 'Failed to fetch reminder count:', e);
            return null;
        }
    },

    color: async () => {
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

    uptime: async () => {
        const uptimeMs = Date.now() - START_TIME;
        const hours = Math.floor(uptimeMs / 3600000);
        const minutes = Math.floor((uptimeMs % 3600000) / 60000);
        return {
            name: 'Custom Status', 
            type: ActivityType.Custom, 
            state: `Uptime: ${hours}h ${minutes}m`
        };
    },

    latency: async (client) => {
        const ping = Math.round(client.ws.ping);
        if (ping < 0) return null;
        return {
            name: 'Custom Status',
            type: ActivityType.Custom,
            state: `Ping: ${ping}ms`
        };
    },

    time: async () => {
        const now = new Date();
        const timeStr = now.toISOString().split('T')[1].substring(0, 5);
        return {
            name: 'Custom Status',
            type: ActivityType.Custom,
            state: `Time: ${timeStr} UTC`
        };
    }
};

const statusList = Object.values(generators);

async function updateStatus(client) {
    // Pick random
    const generator = statusList[Math.floor(Math.random() * statusList.length)];
    let presenceData = await generator(client);

    // Fallback: Uptime
    if (!presenceData) {
        presenceData = await generators.uptime(client);
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
        client.user.setPresence({ status: 'dnd' });
    }
}

let lastActivityTime = Date.now();
let rotationInterval = null;

module.exports = {
    start: (client) => {
        ConsoleLogger.info('StatusRotator', 'Script started.');
        
        // Clear any existing interval
        if (rotationInterval) {
            clearInterval(rotationInterval);
        }
        
        updateStatus(client);
        rotationInterval = setInterval(() => updateStatus(client), ROTATION_INTERVAL);
    },
    
    stop: () => {
        if (rotationInterval) {
            clearInterval(rotationInterval);
            rotationInterval = null;
            ConsoleLogger.info('StatusRotator', 'Script stopped.');
        }
    },
    
    recordActivity: (client) => {
        const timeSinceLastActivity = Date.now() - lastActivityTime;
        const wasIdle = timeSinceLastActivity >= IDLE_THRESHOLD;

        lastActivityTime = Date.now();

        if (wasIdle) {
            ConsoleLogger.info('StatusRotator', 'Waking up from IDLE, forcing status update.');
            updateStatus(client);
        }
    },
    updateStatus: updateStatus
};

