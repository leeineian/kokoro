/**
 * Cat Command Helpers
 * Shared utilities for cat subcommands
 */

// ANSI color codes
const ANSI_COLORS = {
    'gray': '\u001b[30m',
    'red': '\u001b[31m',
    'green': '\u001b[32m',
    'yellow': '\u001b[33m',
    'blue': '\u001b[34m',
    'pink': '\u001b[35m',
    'cyan': '\u001b[36m',
    'white': '\u001b[37m'
};

const ANSI_RESET = '\u001b[0m';

/**
 * Calculate visual width accounting for wide Unicode characters
 * @param {string} text - Text to measure
 * @returns {number} - Visual width in columns
 */
function getVisualWidth(text) {
    // Strip ANSI escape codes first (they don't contribute to visual width)
    const strippedText = text.replace(/\u001b\[\d+m/g, '');
    
    let width = 0;
    for (const char of strippedText) {
        const code = char.codePointAt(0);
        
        // Emoji ranges (excluding mathematical alphanumeric which are single-width)
        if ((code >= 0x1F600 && code <= 0x1F64F) || // Emoticons
            (code >= 0x1F300 && code <= 0x1F5FF) || // Misc Symbols and Pictographs
            (code >= 0x1F680 && code <= 0x1F6FF) || // Transport and Map
            (code >= 0x1F1E0 && code <= 0x1F1FF) || // Flags
            (code >= 0x2600 && code <= 0x26FF) ||   // Misc symbols
            (code >= 0x2700 && code <= 0x27BF) ||   // Dingbats
            (code >= 0x1F900 && code <= 0x1F9FF) || // Supplemental Symbols
            (code >= 0x1FA70 && code <= 0x1FAFF)) { // Extended Pictographic
            width += 2;
        }
        // CJK and other wide characters
        else if ((code >= 0x3000 && code <= 0x9FFF) || // CJK
                    (code >= 0xAC00 && code <= 0xD7AF)) { // Hangul
            width += 2;
        }
        // Full-width alphanumeric and special characters
        else if ((code >= 0xFF01 && code <= 0xFF60) || 
                    (code >= 0xFFE0 && code <= 0xFFE6)) {
            width += 2;
        }
        // Everything else (mathematical alphanumeric are width 1 in Discord's ANSI)
        else {
            width += 1;
        }
    }
    return width;
}

/**
 * Wrap text to fit within maximum width, handling long words gracefully
 * @param {string} text - Text to wrap
 * @param {number} maxWidth - Maximum width in columns
 * @returns {string[]} - Array of wrapped lines
 */
function wrapText(text, maxWidth) {
    const words = text.split(' ');
    const lines = [];
    let currentLine = '';

    for (const word of words) {
        // Handle extremely long words by forcefully splitting them
        if (getVisualWidth(word) > maxWidth) {
            // Push current line if it exists
            if (currentLine) {
                lines.push(currentLine);
                currentLine = '';
            }
            
            // Split the long word into chunks
            let remainingWord = word;
            while (remainingWord.length > 0) {
                let chunk = '';
                let width = 0;
                
                for (const char of remainingWord) {
                    const charWidth = getVisualWidth(char);
                    if (width + charWidth <= maxWidth) {
                        chunk += char;
                        width += charWidth;
                    } else {
                        break;
                    }
                }
                
                if (chunk) {
                    lines.push(chunk);
                    remainingWord = remainingWord.slice(chunk.length);
                } else {
                    // Safety: if we can't fit even one character, break
                    break;
                }
            }
            continue;
        }
        
        const testLine = (currentLine + ' ' + word).trim();
        if (getVisualWidth(testLine) <= maxWidth) {
            currentLine = testLine;
        } else {
            if (currentLine) lines.push(currentLine);
            currentLine = word;
        }
    }
    
    if (currentLine) lines.push(currentLine);
    return lines.length > 0 ? lines : [''];
}

module.exports = {
    ANSI_COLORS,
    ANSI_RESET,
    getVisualWidth,
    wrapText
};
