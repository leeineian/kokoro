// load .env
const { REST, Routes } = require('discord.js');
const fs = require('fs');
const path = require('path');
const ConsoleLogger = require('./utils/log/consoleLogger');

// --- ENVIRONMENT VALIDATION ---
const REQUIRED_ENV_VARS = ['DISCORD_TOKEN', 'CLIENT_ID'];

const missing = REQUIRED_ENV_VARS.filter(key => !process.env[key]);
if (missing.length > 0) {
    ConsoleLogger.error('Sync', `Missing required environment variables: ${missing.join(', ')}`);
    ConsoleLogger.error('Sync', 'Please check your .env file and ensure all required variables are set.');
    process.exit(1);
}


const deployCommands = async () => {
    try {
        const commands = [];
        const commandsPath = path.join(__dirname, 'commands');
        const commandFiles = fs.readdirSync(commandsPath);

        for (const file of commandFiles) {
            const filePath = path.join(commandsPath, file);
            let command;

            if (fs.statSync(filePath).isDirectory()) {
                const indexFile = path.join(filePath, 'index.js');
                if (fs.existsSync(indexFile)) {
                    command = require(indexFile);
                } else {
                    continue;
                }
            } else if (file.endsWith('.js')) {
                 command = require(filePath);
            } else {
                continue;
            }

            if ('data' in command && 'execute' in command) {
                commands.push(command.data.toJSON());
            } else {
                ConsoleLogger.warn('Sync', `The command at ${file} is missing a required "data" or "execute" property.`);
            }
        }

        const rest = new REST({ version: '10' }).setToken(process.env.DISCORD_TOKEN);

        ConsoleLogger.info('Sync', `Started refreshing ${commands.length} application (/) commands.`);

        const data = await rest.put(
            Routes.applicationCommands(process.env.CLIENT_ID),
            { body: commands },
        );

        ConsoleLogger.success('Sync', `Successfully reloaded ${data.length} application (/) commands.`);
        process.exit(0);
    } catch (error) {
        ConsoleLogger.error('Sync', 'Error deploying commands:', error);
        process.exit(1);
    }
};

(async () => {
    await deployCommands();
})();
