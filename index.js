require('dotenv').config();
const { Client, Collection, Events, GatewayIntentBits } = require('discord.js');

const client = new Client({ intents: [GatewayIntentBits.Guilds] });

client.once(Events.ClientReady, c => {
	console.log(`Ready! Logged in as ${c.user.tag}`);
});

client.on(Events.InteractionCreate, async interaction => {
	if (!interaction.isChatInputCommand()) return;

	if (interaction.commandName === 'say') {
		const userInput = interaction.options.getString('message');
		await interaction.reply({ content: userInput, ephemeral: true });
	}
});

client.login(process.env.DISCORD_TOKEN);
