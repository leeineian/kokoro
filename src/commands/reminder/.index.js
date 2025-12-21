const { SlashCommandBuilder, MessageFlags } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const setHandler = require('./set');
const listHandler = require('./list');
const { buildReminderListUI } = require('./.helper');

// Pagination constants shared across handlers
const REMINDERS_PER_PAGE = 25;
const MESSAGE_TRUNCATE_LENGTH = 80;

/**
 * Reminder Command - Main command router and button handlers
 * Manages reminder creation, listing, and interactive dismissal with pagination
 */
module.exports = {
	data: new SlashCommandBuilder()
		.setName('reminder')
		.setDescription('Manage your reminders')
        .addSubcommand(subcommand =>
            subcommand
                .setName('set')
                .setDescription('Set a new reminder')
                .addStringOption(option => 
                    option.setName('message')
                        .setDescription('What should I remind you about?')
                        .setRequired(true))
                .addStringOption(option => 
                    option.setName('when')
                        .setDescription('When? (e.g. "tomorrow at 9am", "in 30 mins")')
                        .setRequired(true))
                .addStringOption(option =>
                    option.setName('sendto')
                        .setDescription('Where should I send the reminder?')
                        .setRequired(false)
                        .addChoices(
                            { name: 'Direct Message (Default)', value: 'dm' },
                            { name: 'This Channel', value: 'channel' }
                        )))
        .addSubcommand(subcommand =>
            subcommand
                .setName('list')
                .setDescription('List your active reminders')),
	/**
	 * Routes the interaction to the appropriate subcommand handler
	 * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
	 * @param {import('discord.js').Client} client - Discord client instance
	 * @returns {Promise<void>}
	 */
	async execute(interaction, client) {
		try {
			const subcommand = interaction.options.getSubcommand();

			switch (subcommand) {
				case 'set':
					return await setHandler.handle(interaction);
				case 'list':
					return await listHandler.handle(interaction);
				default:
					ConsoleLogger.warn('ReminderCommand', `Unknown subcommand: ${subcommand}`);
					await interaction.reply({
						content: 'Unknown reminder command! 📅',
						flags: MessageFlags.Ephemeral
					});
					return;
			}
		} catch (error) {
			ConsoleLogger.error('ReminderCommand', 'Failed to execute reminder command:', error);
			
			// Check if we can still reply
			if (!interaction.replied && !interaction.deferred) {
				await interaction.reply({
					content: 'Something went wrong with the reminder command! 📅',
					flags: MessageFlags.Ephemeral
				});
			}
		}
	},
    handlers: {
        'dismiss_message': async (interaction) => {
             try {
                 // Acknowledge the interaction silently
                 await interaction.deferUpdate();
                 
                 // Delete the message via the channel
                 const channel = await interaction.client.channels.fetch(interaction.channelId);
                 await channel.messages.delete(interaction.message.id);
             } catch (error) {
                 const ConsoleLogger = require('../../utils/log/consoleLogger');
                 ConsoleLogger.error('Reminder', 'Failed to dismiss message:', error);
             }
         },
        
        // Handle refresh button
        'reminder_refresh': async (interaction, customId) => {
            const db = require('../../utils/core/database');
            const ConsoleLogger = require('../../utils/log/consoleLogger');

            try {
                // Extract page number from custom_id
                const pageMatch = customId.match(/reminder_refresh_(\d+)/);
                const currentPage = pageMatch ? parseInt(pageMatch[1]) : 0;

                // Get user reminders
                const userReminders = db.getReminders(interaction.user.id);

                if (userReminders.length === 0) {
                    return interaction.update({
                        content: 'You have no confirmed reminders.',
                        components: [],
                        flags: MessageFlags.IsComponentsV2
                    });
                }

                // Build UI using shared helper
                const { v2Container } = buildReminderListUI(userReminders, currentPage);

                await interaction.update({
                    content: null,
                    flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral,
                    components: [v2Container]
                });

            } catch (err) {
                ConsoleLogger.error('ReminderCommand', 'Refresh failed:', err);
                await interaction.reply({ 
                    content: 'Failed to refresh reminder list.', 
                    flags: MessageFlags.Ephemeral 
                });
            }
        },
        
        // Handle reminder dismissal from select menu
        'dismiss_reminder': async (interaction, customId) => {
            const db = require('../../utils/core/database');
            const reminderScheduler = require('../../daemons/reminderScheduler');
            const ConsoleLogger = require('../../utils/log/consoleLogger');

            try {
                // Extract page number from custom_id (e.g., "dismiss_reminder_page_2")
                const pageMatch = customId.match(/dismiss_reminder_page_(\d+)/);
                const currentPage = pageMatch ? parseInt(pageMatch[1]) : 0;
                
                // Get selected reminder ID from interaction values
                const reminderId = interaction.values?.[0];
                if (!reminderId) {
                    return interaction.reply({ 
                        content: 'No reminder selected.', 
                        flags: MessageFlags.Ephemeral 
                    });
                }

                // Delete the reminder
                const deleted = db.deleteReminder(parseInt(reminderId));
                
                if (deleted === 0) {
                    return interaction.reply({ 
                        content: 'Reminder not found or already deleted.', 
                        flags: MessageFlags.Ephemeral 
                    });
                }

                // Cancel the scheduled job
                reminderScheduler.cancelReminder(parseInt(reminderId));
                
                ConsoleLogger.info('Reminder', `User ${interaction.user.id} dismissed reminder ${reminderId}`);

                // Rebuild the list with updated data
                const userReminders = db.getReminders(interaction.user.id);

                if (userReminders.length === 0) {
                    // No more reminders
                    return interaction.update({
                        content: '✅ Reminder dismissed. You have no more active reminders.',
                        components: []
                    });
                }

                // Calculate new page (if we deleted last item, go back a page)
                const REMINDERS_PER_PAGE = 25;
                const totalPages = Math.ceil(userReminders.length / REMINDERS_PER_PAGE);
                const newPage = currentPage >= totalPages ? Math.max(0, totalPages - 1) : currentPage;

                // Build UI using shared helper
                const { v2Container } = buildReminderListUI(userReminders, newPage);

                await interaction.update({
                    content: null,
                    flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral,
                    components: [v2Container]
                });

            } catch (err) {
                ConsoleLogger.error('ReminderCommand', 'Dismiss reminder failed:', err);
                await interaction.reply({ 
                    content: 'Failed to dismiss reminder.', 
                    flags: MessageFlags.Ephemeral 
                });
            }
        },

        // Handle previous/next page navigation
        'reminder_page_nav': async (interaction, customId) => {
            const db = require('../../utils/core/database');
            const ConsoleLogger = require('../../utils/log/consoleLogger');

            try {
                // Extract direction and current page from custom_id
                const prevMatch = customId.match(/reminder_page_prev_(\d+)/);
                const nextMatch = customId.match(/reminder_page_next_(\d+)/);
                
                const currentPage = prevMatch ? parseInt(prevMatch[1]) : (nextMatch ? parseInt(nextMatch[1]) : 0);
                const newPage = prevMatch ? currentPage - 1 : currentPage + 1;

                // Get user reminders
                const userReminders = db.getReminders(interaction.user.id);

                if (userReminders.length === 0) {
                    return interaction.update({
                        content: 'You have no confirmed reminders.',
                        components: []
                    });
                }

                // Build UI using shared helper
                const { v2Container } = buildReminderListUI(userReminders, newPage);

                await interaction.update({
                    content: null,
                    flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral,
                    components: [v2Container]
                });

            } catch (err) {
                ConsoleLogger.error('ReminderCommand', 'Page navigation failed:', err);
                await interaction.reply({ 
                    content: 'Failed to navigate pages.', 
                    flags: MessageFlags.Ephemeral 
                });
            }
        }
    }
};
