const { Events, MessageFlags } = require('discord.js');
const ConsoleLogger = require('../utils/log/consoleLogger');
const statusRotator = require('../daemons/statusRotator');
const { logAction, getLoggingConfig } = require('../utils/log/auditLogger');

// Error messages
const ERRORS = {
    GENERIC: 'There was an error while executing this command!',
    GENERIC_BUTTON: 'There was an error while executing this button!',
    GENERIC_MENU: 'Error processing interaction.',
    BUTTON_INACTIVE: 'This button is no longer active.',
    INVALID_INTERACTION: 'This interaction is no longer valid.',
};

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
                let handler = client.componentHandlers.get(interaction.customId);
                
                // If no exact match, try pattern matching
                if (!handler) {
                    for (const [key, value] of client.componentHandlers) {
                        // Check for pattern-based handlers
                        if (interaction.customId.startsWith('reminder_page_')) {
                            if (key === 'reminder_page_nav') {
                                handler = value;
                                break;
                            }
                        } else if (interaction.customId.startsWith('reminder_refresh_')) {
                            if (key === 'reminder_refresh') {
                                handler = value;
                                break;
                            }
                        }
                    }
                }
                
                if (!handler) {
                    ConsoleLogger.error('Interaction', `No handler matching ${interaction.customId} was found.`);
                    await interaction.reply({ content: ERRORS.BUTTON_INACTIVE, flags: MessageFlags.Ephemeral });
                    return;
                }

                try {
                    await handler(interaction, interaction.customId);
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
                    let handler = client.componentHandlers.get(interaction.customId);
                    
                    // If no exact match, try pattern matching
                    if (!handler) {
                        for (const [key, value] of client.componentHandlers) {
                            // Check for pattern-based handlers (e.g., "dismiss_reminder_page_*")
                            if (interaction.customId.startsWith('dismiss_reminder_page_')) {
                                if (key === 'dismiss_reminder') {
                                    handler = value;
                                    break;
                                }
                            }
                            // Webhook looper exact match handlers
                            if (interaction.customId === 'delete_loop_config' && key === 'delete_loop_config') {
                                handler = value;
                                break;
                            }
                            if (interaction.customId === 'stop_loop_select' && key === 'stop_loop_select') {
                                handler = value;
                                break;
                            }
                        }
                    }
                    
                    if (!handler) {
                        ConsoleLogger.error('Interaction', `No handler matching ${interaction.customId} was found.`);
                        await interaction.reply({ content: ERRORS.INVALID_INTERACTION, flags: MessageFlags.Ephemeral });
                        return;
                    }

                    try {
                        await handler(interaction, interaction.customId);
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
