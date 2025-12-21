// load .env
const { REST, Routes } = require('discord.js');
const fs = require('fs');
const path = require('path');
const ConsoleLogger = require('./utils/consoleLogger');

const deployCommands = async () => {
    try {
        const commands = [];
        const commandsPath = path.join(__dirname, 'commands');
        const commandFiles = fs.readdirSync(commandsPath).filter(file => file.endsWith('.js'));

        for (const file of commandFiles) {
            const command = require(path.join(commandsPath, file));
            if ('data' in command && 'execute' in command) {
                commands.push(command.data.toJSON());
            } else {
                ConsoleLogger.warn('Deploy', `The command at ${file} is missing a required "data" or "execute" property.`);
            }
        }

        const rest = new REST({ version: '10' }).setToken(process.env.DISCORD_TOKEN);

        ConsoleLogger.info('Deploy', `Started refreshing ${commands.length} application (/) commands.`);

        const data = await rest.put(
            Routes.applicationCommands(process.env.CLIENT_ID),
            { body: commands },
        );

        ConsoleLogger.success('Deploy', `Successfully reloaded ${data.length} application (/) commands.`);
    } catch (error) {
        ConsoleLogger.error('Deploy', 'Error deploying commands:', error);
    }
};

(async () => {
    await deployCommands();
})();
