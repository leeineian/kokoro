// load .env
const { performance } = require('perf_hooks');
const fs = require('fs');
const path = require('path');

// Discord.js
const { Client, Collection, Events, GatewayIntentBits, MessageFlags, REST, Routes } = require('discord.js');
const chalk = require('chalk');

// Utilities
const { logAction, getLoggingConfig } = require('./utils/logger');
const V2Builder = require('./utils/components');
const db = require('./utils/database');

// Commands & Scripts
const reminderCommand = require('./commands/reminder');
const randomRoleColor = require('./scripts/randomRoleColor');
const statusRotator = require('./scripts/statusRotator');

const startTime = performance.now();

// --- DEPLOYMENT LOGIC START ---
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
                console.log(chalk.yellow(`[WARNING] The command at ${file} is missing a required "data" or "execute" property.`));
            }
        }

        const rest = new REST({ version: '10' }).setToken(process.env.DISCORD_TOKEN);

        console.log(`Started refreshing ${commands.length} application (/) commands.`);

        if (process.env.GUILD_ID) {
            // Optional: Guild commands
        }

        console.log('Registering commands globally');
        const data = await rest.put(
            Routes.applicationCommands(process.env.CLIENT_ID),
            { body: commands },
        );

        console.log(`Successfully reloaded ${data.length} application (/) commands.`);
    } catch (error) {
        console.error(error);
    }
};
// --- DEPLOYMENT LOGIC END ---

const client = new Client({ 
    intents: [GatewayIntentBits.Guilds],
    presence: { status: 'dnd' }
});

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
		console.log(chalk.yellow(`[WARNING] The command at ${filePath} is missing a required "data" or "execute" property.`));
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
            console.log(chalk.yellow(`[WARNING] The button at ${filePath} is missing a required "customId" or "execute" property.`));
        }
    }
}

client.once(Events.ClientReady, async c => {
    const duration = (performance.now() - startTime).toFixed(2);
	console.log(chalk.green(`Ready! Logged in as ${c.user.tag} (Startup: ${duration}ms)`));

    // Start Background Scripts
    statusRotator.start(client);
    randomRoleColor.start(client);

    // Restore Reminders
    try {
        // Synchronous call - no await
        const pending = db.getAllPendingReminders();
        console.log(chalk.blue(`Restoring ${pending.length} pending reminders...`));
        
        let restoredCount = 0;
        const now = Date.now();

        for (const r of pending) {
            // Restore using safe scheduler
            reminderCommand.scheduleReminder(client, r.userId, r.channelId, r.message, r.id, r.deliveryType, r.dueAt);
            restoredCount++;
        }
        console.log(chalk.blue(`Restored ${restoredCount} reminders.`));
    } catch (err) {
        console.error(chalk.red('Failed to restore reminders:'), err);
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
        
        // Dynamic Status
        statusRotator.recordActivity(client);
        
        // Button Handling
        if (interaction.isButton()) {
            const button = client.buttons.get(interaction.customId);
            if (!button) {
                console.error(chalk.red(`No handler matching ${interaction.customId} was found.`));
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
            console.error(chalk.red(`No command matching ${interaction.commandName} was found.`));
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
        const config = getLoggingConfig();
        if (interaction.guildId && config[interaction.guildId]?.enabled) {
            const finalDetails = logDetails || formatCommandOptions(interaction);
            logAction(client, interaction.guildId, interaction.user, `Used /${interaction.commandName}`, finalDetails);
        }

    } catch (error) {
        console.error(chalk.red('Uncaptured interaction error:'), error);
    }
});

(async () => {
    await deployCommands();
    await client.login(process.env.DISCORD_TOKEN);
})();
