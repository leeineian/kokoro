// load .env
const { performance } = require('perf_hooks');
const fs = require('fs');
const path = require('path');

// Discord.js
const { Client, Collection, GatewayIntentBits } = require('discord.js');
const ConsoleLogger = require('./utils/consoleLogger');

// Utilities

const startTime = performance.now();

// --- PROCESS MANAGEMENT START ---
const { PATHS } = require('./configs/constants');
const PID_FILE = PATHS.PID_FILE;

try {
    fs.writeFileSync(PID_FILE, process.pid.toString());
    ConsoleLogger.info('Start', `PID file created: ${process.pid}`);
} catch (err) {
    ConsoleLogger.error('Start', 'Failed to create PID file:', err);
}

const cleanup = () => {
    try {
        if (fs.existsSync(PID_FILE)) {
            fs.unlinkSync(PID_FILE);
            ConsoleLogger.info('Shutdown', 'PID file removed.');
        }
    } catch (err) {
        ConsoleLogger.error('Shutdown', 'Failed to remove PID file:', err);
    }
    process.exit(0);
};

process.on('SIGINT', cleanup);
process.on('SIGTERM', cleanup);
process.on('unhandledRejection', (reason, promise) => {
    ConsoleLogger.error('Fatal', `Unhandled Rejection at: ${promise}`, reason);
});
process.on('uncaughtException', (err) => {
    ConsoleLogger.error('Fatal', 'Uncaught Exception:', err);
});
// --- PROCESS MANAGEMENT END ---

const client = new Client({ 
    intents: [
        GatewayIntentBits.Guilds,
        GatewayIntentBits.GuildMessages,
        GatewayIntentBits.MessageContent
    ],
    presence: { status: 'dnd' }
});

// Handler Loading
client.commands = new Collection();
client.componentHandlers = new Collection();

const commandsPath = path.join(__dirname, 'commands');
const commandFiles = fs.readdirSync(commandsPath);

for (const file of commandFiles) {
	const filePath = path.join(commandsPath, file);
    let command;

    try {
        if (fs.statSync(filePath).isDirectory()) {
            const indexFile = path.join(filePath, 'index.js');
            if (fs.existsSync(indexFile)) {
                command = require(indexFile);
            } else {
                continue; // Skip directories without index.js
            }
        } else if (file.endsWith('.js')) {
            command = require(filePath);
        } else {
            continue; 
        }

        if ('data' in command && 'execute' in command) {
            client.commands.set(command.data.name, command);
            
            // Register component handlers if present
            if (command.handlers) {
                for (const [customId, handler] of Object.entries(command.handlers)) {
                    client.componentHandlers.set(customId, handler);
                }
            }
        } else {
            ConsoleLogger.warn('Loader', `The command at ${filePath} is missing a required "data" or "execute" property.`);
        }
    } catch (err) {
        ConsoleLogger.error('Loader', `Failed to load command ${file}:`, err);
    }
}

// Event Handling
const eventsPath = path.join(__dirname, 'events');
const eventFiles = fs.readdirSync(eventsPath).filter(file => file.endsWith('.js'));

for (const file of eventFiles) {
	const filePath = path.join(eventsPath, file);
    try {
        const event = require(filePath);
        if (event.once) {
            client.once(event.name, (...args) => event.execute(...args));
        } else {
            client.on(event.name, (...args) => event.execute(...args));
        }
    } catch (err) {
        ConsoleLogger.error('Loader', `Failed to load event ${file}:`, err);
    }
}

(async () => {
    await client.login(process.env.DISCORD_TOKEN);
})();
