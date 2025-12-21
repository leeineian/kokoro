const { MessageFlags } = require('discord.js');
const V2Builder = require('../../utils/core/components');

// Import shared constants from index
const REMINDERS_PER_PAGE = 25;
const MESSAGE_TRUNCATE_LENGTH = 80;

/**
 * Reminder List Helper - Shared pagination and UI building logic
 * Used by multiple button handlers to avoid code duplication
 */

/**
 * Builds the reminder list UI with pagination
 * @param {Array} userReminders - All user reminders from database
 * @param {number} currentPage - Current page number (0-indexed)
 * @returns {Object} - Object containing v2Container, safePage, and totalPages
 */
function buildReminderListUI(userReminders, currentPage) {
    // Calculate pagination
    const totalPages = Math.ceil(userReminders.length / REMINDERS_PER_PAGE);
    const safePage = Math.max(0, Math.min(currentPage, totalPages - 1));

    const startIdx = safePage * REMINDERS_PER_PAGE;
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
            V2Builder.button('🔄', `reminder_refresh_${safePage}`, 2)
        )
    );

    // Add select menu
    components.push(
        V2Builder.actionRow([
            V2Builder.selectMenu(
                `dismiss_reminder_page_${safePage}`,
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
        
        if (safePage > 0) {
            paginationButtons.push(
                V2Builder.button('◀ Previous', `reminder_page_prev_${safePage}`, 1)
            );
        }
        
        if (safePage < totalPages - 1) {
            paginationButtons.push(
                V2Builder.button('Next ▶', `reminder_page_next_${safePage}`, 1)
            );
        }

        if (paginationButtons.length > 0) {
            components.push(V2Builder.actionRow(paginationButtons));
        }
    }

    const v2Container = V2Builder.container(components);

    return {
        v2Container,
        safePage,
        totalPages
    };
}

module.exports = {
    buildReminderListUI
};
