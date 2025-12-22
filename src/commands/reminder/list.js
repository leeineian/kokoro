const { MessageFlags } = require('discord.js');
const V2Builder = require('../../utils/core/components');
const db = require('../../utils/core/database');
const ConsoleLogger = require('../../utils/log/consoleLogger');

// Pagination constants
const REMINDERS_PER_PAGE = 25;
const MESSAGE_TRUNCATE_LENGTH = 80;
const MAX_PAGES = 10; // Safety limit for pagination

/**
 * Reminder List Handler - Displays user's active reminders with pagination
 * Supports interactive dismissal via select menu and refresh functionality
 */
module.exports = {
    /**
     * Handles reminder list display with pagination
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @param {number} [page=0] - Page number for pagination
     * @returns {Promise<void>}
     */
    async handle(interaction, page = 0) {
        try {
            // Validate page number
            page = Math.max(0, Math.min(page, MAX_PAGES - 1));

            // Synchronous call - no await
            const userReminders = db.getReminders(interaction.user.id);

            if (userReminders.length === 0) {
                return interaction.reply({ 
                    content: 'You have no confirmed reminders.', 
                    flags: MessageFlags.Ephemeral 
                });
            }

            // Pagination logic
            const totalPages = Math.ceil(userReminders.length / REMINDERS_PER_PAGE);
            const currentPage = Math.max(0, Math.min(page, totalPages - 1)); // Clamp page
            
            // Additional safety check for excessive pages
            if (totalPages > MAX_PAGES) {
                ConsoleLogger.warn('Reminder', `User ${interaction.user.tag} has excessive reminders (${userReminders.length})`);
            }
            
            const startIdx = currentPage * REMINDERS_PER_PAGE;
            const endIdx = Math.min(startIdx + REMINDERS_PER_PAGE, userReminders.length);
            const pageReminders = userReminders.slice(startIdx, endIdx);

            // Build text list (showing all reminders)
            const listText = userReminders.map((r, i) => 
                `${i+1}. "${r.message}" <t:${Math.floor(r.dueAt/1000)}:R> (${r.deliveryType === 'channel' && r.channelId ? `<#${r.channelId}>` : 'DM'})`
            ).join('\n');

            // Build select menu options (only current page)
            const selectOptions = pageReminders.map(r => {
                const truncatedMessage = r.message.length > MESSAGE_TRUNCATE_LENGTH 
                    ? r.message.substring(0, MESSAGE_TRUNCATE_LENGTH - 3) + '...' 
                    : r.message;
                
                return {
                    label: truncatedMessage,
                    value: r.id.toString(),
                    description: `Due ${new Date(r.dueAt).toLocaleString()}`
                };
            });

            // Build components
            const components = [];
            
            // Add section with text and refresh button accessory
            components.push(
                V2Builder.section(
                    [V2Builder.textDisplay(`**Your Reminders**\n${listText}`)],
                    V2Builder.button('🔄', `reminder_refresh_${currentPage}`, 2) // Refresh button on the right
                )
            );

            // Add select menu
            components.push(
                V2Builder.actionRow([
                    V2Builder.selectMenu(
                        `dismiss_reminder_page_${currentPage}`,
                        selectOptions,
                        'Select a reminder to dismiss',
                        1,
                        1
                    )
                ])
            );

            // Add pagination buttons if needed
            if (totalPages > 1) {
                const paginationButtons = [];
                
                // Previous button
                if (currentPage > 0) {
                    paginationButtons.push(
                        V2Builder.button('◀ Previous', `reminder_page_prev_${currentPage}`, 1)
                    );
                }
                
                // Next button
                if (currentPage < totalPages - 1) {
                    paginationButtons.push(
                        V2Builder.button('Next ▶', `reminder_page_next_${currentPage}`, 1)
                    );
                }

                if (paginationButtons.length > 0) {
                    components.push(V2Builder.actionRow(paginationButtons));
                }
            }

            const v2Container = V2Builder.container(components);
            
            // Ephemeral List
            await interaction.reply({
                content: null,
                flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral,
                components: [v2Container]
            });

        } catch (error) {
            ConsoleLogger.error('Reminder', 'Failed to list reminders:', error);
            await interaction.reply({ 
                content: 'Database error.', 
                flags: MessageFlags.Ephemeral 
            });
        }
    }
};
