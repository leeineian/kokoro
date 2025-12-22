const { MessageFlags } = require('discord.js');
const V2Builder = require('../../utils/core/components');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const { sanitizeInput, validateLength, validateColor, ALLOWED_COLORS } = require('../.validation');

// Configuration constants
const { ANSI_COLORS, ANSI_RESET, getVisualWidth, wrapText } = require('./.helper');
const MAX_MESSAGE_LENGTH = 280;
const MIN_MESSAGE_LENGTH = 1;
const BUBBLE_TEXT_WIDTH = 30;
const CAT_ASCII_WIDTH = 13;

/**
 * Cat Say Command - Generates ASCII art speech bubbles with a cat
 * Supports custom colors for message text, bubble borders, and the cat itself
 */
module.exports = {
    /**
     * Executes the cat say command
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<string|void>} - Returns status message or void on error
     */
    async execute(interaction) {
        try {
            const message = interaction.options.getString('message');
            const msgColor = interaction.options.getString('msgcolor');
            const bubColor = interaction.options.getString('bubcolor');
            const catColor = interaction.options.getString('catcolor');
            
            // Validate message is not empty/whitespace only
            if (!message || message.trim().length === 0) {
                return interaction.reply({
                    content: 'Message cannot be empty!',
                    flags: MessageFlags.Ephemeral
                });
            }
            
            // Validate message length
            if (!validateLength(message, MIN_MESSAGE_LENGTH, MAX_MESSAGE_LENGTH)) {
                return interaction.reply({
                    content: `Message must be between ${MIN_MESSAGE_LENGTH} and ${MAX_MESSAGE_LENGTH} characters.`,
                    flags: MessageFlags.Ephemeral
                });
            }

            // Sanitize message (prevent ANSI injection, but allow newlines for formatting)
            const sanitizedMessage = sanitizeInput(message, { allowNewlines: true, allowAnsi: false });
            
            // Additional check - ensure sanitized message isn't empty after cleaning
            if (sanitizedMessage.trim().length === 0) {
                return interaction.reply({
                    content: '⚠️ Your message contains only invalid characters!',
                    flags: MessageFlags.Ephemeral
                });
            }

            // Validate color choices
            if (!validateColor(msgColor, ALLOWED_COLORS)) {
                return interaction.reply({
                    content: `Invalid message color! Allowed colors: ${ALLOWED_COLORS.join(', ')}`,
                    flags: MessageFlags.Ephemeral
                });
            }
            
            if (!validateColor(bubColor, ALLOWED_COLORS)) {
                return interaction.reply({
                    content: `Invalid bubble color! Allowed colors: ${ALLOWED_COLORS.join(', ')}`,
                    flags: MessageFlags.Ephemeral
                });
            }
            
            if (!validateColor(catColor, ALLOWED_COLORS)) {
                return interaction.reply({
                    content: `Invalid cat color! Allowed colors: ${ALLOWED_COLORS.join(', ')}`,
                    flags: MessageFlags.Ephemeral
                });
            }

            // Build the speech bubble
            const lines = wrapText(sanitizedMessage, BUBBLE_TEXT_WIDTH);
            const maxLen = Math.max(...lines.map(l => getVisualWidth(l)));
            
            // Apply bubble color if specified
            const bubColorCode = bubColor ? ANSI_COLORS[bubColor] : '';
            const bubReset = bubColor ? ANSI_RESET : '';
            
            const topBorder = bubColorCode + ' ' + '_'.repeat(maxLen + 2) + bubReset;
            const bottomBorder = bubColorCode + ' ' + '-'.repeat(maxLen + 2) + bubReset;
            
            // Apply message color if specified
            const msgColorCode = msgColor ? ANSI_COLORS[msgColor] : '';
            const msgReset = msgColor ? ANSI_RESET : '';
            
            const bubble = [
                topBorder,
                ...lines.map((line, i) => {
                    const lineWidth = getVisualWidth(line);
                    const padding = ' '.repeat(maxLen - lineWidth);
                    const coloredLine = msgColorCode + line + msgReset + padding;
                    
                    let leftBracket, rightBracket;
                    if (lines.length === 1) {
                        leftBracket = '<';
                        rightBracket = '>';
                    } else if (i === 0) {
                        leftBracket = '/';
                        rightBracket = '\\';
                    } else if (i === lines.length - 1) {
                        leftBracket = '\\';
                        rightBracket = '/';
                    } else {
                        leftBracket = '|';
                        rightBracket = '|';
                    }
                    
                    return bubColorCode + leftBracket + bubReset + ' ' + coloredLine + ' ' + bubColorCode + rightBracket + bubReset;
                }),
                bottomBorder
            ].join('\n');

            // Position cat in the center of bubble
            const bubbleWidth = maxLen + 4; // +4 for border chars and spaces
            const catIndent = ' '.repeat(Math.max(0, Math.floor((bubbleWidth - CAT_ASCII_WIDTH) / 2)));
            
            // Apply cat color if specified
            const catColorCode = catColor ? ANSI_COLORS[catColor] : '';
            const catReset = catColor ? ANSI_RESET : '';
            
            const cat = 
`${catIndent}    ${bubColorCode}\\${bubReset}
${catIndent}     ${bubColorCode}\\${bubReset}
${catIndent}      ${catColorCode}/\\_/\\${catReset}  
${catIndent}     ${catColorCode}( o.o )${catReset} 
${catIndent}      ${catColorCode}> ^ <${catReset}`;

            const output = bubble + '\n' + cat;

            const v2Container = V2Builder.container([
                V2Builder.textDisplay(`\`\`\`ansi\n${output}\n\`\`\``)
            ]);

            await interaction.reply({
                components: [v2Container],
                flags: MessageFlags.IsComponentsV2
            });

            return 'Generated cat say';
        } catch (error) {
            ConsoleLogger.error('CatCommand', 'Failed to generate cat say:', error);
            
            // Check if we can still reply
            if (!interaction.replied && !interaction.deferred) {
                await interaction.reply({
                    content: 'Failed to generate cat speech bubble! 😿',
                    flags: MessageFlags.Ephemeral
                });
            }
        }
    }
};
