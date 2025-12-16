require('dotenv').config();
const fs = require('fs');
const path = require('path');
const { Client, Collection, Events, GatewayIntentBits, ActivityType, PermissionFlagsBits, MessageFlags } = require('discord.js');
const { logAction } = require('./utils/logger');

const V2Builder = require('./utils/components');

const client = new Client({ intents: [GatewayIntentBits.Guilds] });

// Command Handling
client.commands = new Collection();
const commandsPath = path.join(__dirname, 'commands');
const commandFiles = fs.readdirSync(commandsPath).filter(file => file.endsWith('.js'));

for (const file of commandFiles) {
	const filePath = path.join(commandsPath, file);
	const command = require(filePath);
	if ('data' in command && 'execute' in command) {
		client.commands.set(command.data.name, command);
	} else {
		console.log(`[WARNING] The command at ${filePath} is missing a required "data" or "execute" property.`);
	}
}

// Button Handling
client.buttons = new Collection();
const buttonsPath = path.join(__dirname, 'buttons');
if (fs.existsSync(buttonsPath)) {
    const buttonFiles = fs.readdirSync(buttonsPath).filter(file => file.endsWith('.js'));
    for (const file of buttonFiles) {
        const filePath = path.join(buttonsPath, file);
        const button = require(filePath);
        if ('customId' in button && 'execute' in button) {
            client.buttons.set(button.customId, button);
        } else {
            console.log(`[WARNING] The button at ${filePath} is missing a required "customId" or "execute" property.`);
        }
    }
}

client.once(Events.ClientReady, c => {
	console.log(`Ready! Logged in as ${c.user.tag}`);
	client.user.setActivity('Minding my own business', { type: ActivityType.Custom });
});

// Helper to format options for auto-logging
function formatCommandOptions(interaction) {
    if (!interaction.options.data.length) return 'No options provided';
    return interaction.options.data.map(opt => `${opt.name}: ${opt.value}`).join('\n');
}

client.on(Events.InteractionCreate, async interaction => {
    try {
        console.log(`Received interaction: ${interaction.type} (ID: ${interaction.id})`);
        
        // Button Handling
        if (interaction.isButton()) {
            const button = client.buttons.get(interaction.customId);
            if (!button) {
                console.error(`No handler matching ${interaction.customId} was found.`);
                await interaction.reply({ content: 'This button is no longer active.', flags: MessageFlags.Ephemeral });
                return;
            }

            try {
                await button.execute(interaction, client);
            } catch (error) {
                console.error(error);
                if (!interaction.replied && !interaction.deferred) { 
                    await interaction.reply({ content: 'There was an error while executing this button!', flags: MessageFlags.Ephemeral });
                }
            }
            return;
        }

        if (!interaction.isChatInputCommand()) return;

        const command = client.commands.get(interaction.commandName);

        if (!command) {
            console.error(`No command matching ${interaction.commandName} was found.`);
            return;
        }

        // Execute Command
        let logDetails = null;
        try {
            // Commands return string/details if they want to override logging, or null/undefined
            const result = await command.execute(interaction, client);
            logDetails = result;
        } catch (error) {
            console.error(error);
            if (interaction.replied || interaction.deferred) {
                await interaction.followUp({ content: 'There was an error while executing this command!', flags: MessageFlags.Ephemeral });
            } else {
                await interaction.reply({ content: 'There was an error while executing this command!', flags: MessageFlags.Ephemeral });
            }
        }

        // Auto-Logging
        const finalDetails = logDetails || formatCommandOptions(interaction);
        logAction(client, interaction.guildId, interaction.user, `Used /${interaction.commandName}`, finalDetails);

    } catch (error) {
        console.error('Uncaptured interaction error:', error);
    }
});

client.login(process.env.DISCORD_TOKEN);
