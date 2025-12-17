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

const db = require('./utils/database');
const reminderCommand = require('./commands/reminder');
const randomRoleColor = require('./scripts/randomRoleColor');

client.once(Events.ClientReady, async c => {
	console.log(`Ready! Logged in as ${c.user.tag}`);
	client.user.setActivity('Minding my own business', { type: ActivityType.Custom });

    // Start Background Scripts
    randomRoleColor.start(client);

    // Restore Reminders
    try {
        const pending = await db.getAllPendingReminders();
        console.log(`Restoring ${pending.length} pending reminders...`);
        
        let restoredCount = 0;
        const now = Date.now();

        for (const r of pending) {
            const delay = r.dueAt - now;
            if (delay <= 0) {
                // Should have sent already -> Send immediately
                reminderCommand.sendReminder(client, r.userId, r.channelId, r.message, r.id, r.deliveryType);
            } else {
                // Schedule future
                setTimeout(() => {
                    reminderCommand.sendReminder(client, r.userId, r.channelId, r.message, r.id, r.deliveryType);
                }, delay);
            }
            restoredCount++;
        }
        console.log(`Restored ${restoredCount} reminders.`);
    } catch (err) {
        console.error('Failed to restore reminders:', err);
    }
});

// Helper to format options for auto-logging
function formatCommandOptions(interaction) {
    if (!interaction.options.data.length) return 'No options provided';

    const formatOption = (opt) => {
        if (opt.options) {
            // It's a subcommand or group
            const subOptions = opt.options.map(formatOption).join(', ');
            if (!subOptions) return `Subcommand: ${opt.name}`;
            return `${opt.name}: [${subOptions}]`;
        }
        return `${opt.name}: ${opt.value}`;
    };

    return interaction.options.data.map(formatOption).join('\n');
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
