require('dotenv').config();
const { REST, Routes, SlashCommandBuilder } = require('discord.js');

const commands = [
	new SlashCommandBuilder()
		.setName('say')
		.setDescription('Repeats your input back to you.')
		.addStringOption(option =>
			option.setName('message')
				.setDescription('The message to say')
				.setRequired(true)),
]
	.map(command => command.toJSON());

const rest = new REST({ version: '10' }).setToken(process.env.DISCORD_TOKEN);

(async () => {
	try {
		console.log('Started refreshing application (/) commands.');

		// If GUILD_ID is provided, register for guild (instant update), otherwise global (can take 1 hour)
		if (process.env.GUILD_ID) {
            console.log(`Registering commands to Guild: ${process.env.GUILD_ID}`);
			await rest.put(
				Routes.applicationGuildCommands(process.env.CLIENT_ID, process.env.GUILD_ID),
				{ body: commands },
			);
		} else {
            console.log('Registering commands globally');
			await rest.put(
				Routes.applicationCommands(process.env.CLIENT_ID),
				{ body: commands },
			);
		}

		console.log('Successfully reloaded application (/) commands.');
	} catch (error) {
		console.error(error);
	}
})();
