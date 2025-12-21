// load .env
const { REST, Routes } = require('discord.js');
const fs = require('fs');
const path = require('path');
const ConsoleLogger = require('./utils/consoleLogger');

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
    } catch (error) {
        ConsoleLogger.error('Sync', 'Error deploying commands:', error);
    }
};

(async () => {
    await deployCommands();
})();
