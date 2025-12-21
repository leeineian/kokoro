const { Events, MessageFlags } = require('discord.js');
const { ERRORS } = require('../configs/text');
const ConsoleLogger = require('../utils/consoleLogger');
const statusRotator = require('../scripts/statusRotator');
const { logAction, getLoggingConfig } = require('../utils/auditLogger');

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

module.exports = {
    name: Events.InteractionCreate,
    async execute(interaction) {
        const client = interaction.client;

        try {
            ConsoleLogger.debug('Interaction', `Received interaction: ${interaction.type} (ID: ${interaction.id})`);
            
            // Dynamic Status
            statusRotator.recordActivity(client);
            
            // Button Handling
            if (interaction.isButton()) {
                const handler = client.componentHandlers.get(interaction.customId);
                if (!handler) {
                    ConsoleLogger.error('Interaction', `No handler matching ${interaction.customId} was found.`);
                    await interaction.reply({ content: ERRORS.BUTTON_INACTIVE, flags: MessageFlags.Ephemeral });
                    return;
                }

                try {
                    await handler(interaction, client);
                } catch (error) {
                    ConsoleLogger.error('Interaction', 'Button execution error:', error);
                    if (!interaction.replied && !interaction.deferred) { 
                        await interaction.reply({ content: ERRORS.GENERIC_BUTTON, flags: MessageFlags.Ephemeral });
                    }
                }
                return;
            }

            if (!interaction.isChatInputCommand()) {
                // Select Menu Handling (via global handlers)
                if (interaction.isStringSelectMenu()) {
                    const handler = client.componentHandlers.get(interaction.customId);
                    if (!handler) {
                        ConsoleLogger.error('Interaction', `No handler matching ${interaction.customId} was found.`);
                        await interaction.reply({ content: ERRORS.INVALID_INTERACTION, flags: MessageFlags.Ephemeral });
                        return;
                    }

                    try {
                        await handler(interaction, client);
                    } catch (error) {
                        ConsoleLogger.error('Interaction', 'Select menu execution error:', error);
                        if (!interaction.replied && !interaction.deferred) { 
                            await interaction.reply({ content: ERRORS.GENERIC_MENU, flags: MessageFlags.Ephemeral });
                        }
                    }
                    return;
                }
                return;
            }

            const command = client.commands.get(interaction.commandName);

            if (!command) {
                ConsoleLogger.error('Interaction', `No command matching ${interaction.commandName} was found.`);
                return;
            }

            // Execute Command
            let logDetails = null;
            try {
                // Commands return string/details if they want to override logging, or null/undefined
                const result = await command.execute(interaction, client);
                logDetails = result;
            } catch (error) {
                ConsoleLogger.error('Interaction', 'Command execution error:', error);
                if (interaction.replied || interaction.deferred) {
                    await interaction.followUp({ content: ERRORS.GENERIC, flags: MessageFlags.Ephemeral });
                } else {
                    await interaction.reply({ content: ERRORS.GENERIC, flags: MessageFlags.Ephemeral });
                }
            }

            // Auto-Logging
            const config = getLoggingConfig();
            if (interaction.guildId && config[interaction.guildId]?.enabled) {
                const finalDetails = logDetails || formatCommandOptions(interaction);
                logAction(client, interaction.guildId, interaction.user, `Used /${interaction.commandName}`, finalDetails);
            }

        } catch (error) {
            ConsoleLogger.error('Interaction', 'Uncaptured interaction error:', error);
        }
    },
};
