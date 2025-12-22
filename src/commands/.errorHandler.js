/**
 * Centralized Error Handler
 * 
 * @module commands/errorHandler
 * @description Provides standardized error handling, categorization, and recovery mechanisms
 * for all commands. This module ensures consistent error messages and appropriate handling
 * based on error type.
 * 
 * @example
 * const { handleCommandError, retryOperation, isInteractionValid } = require('./.errorHandler');
 * 
 * // Handle error with automatic categorization
 * try {
 *     await riskyOperation();
 * } catch (error) {
 *     await handleCommandError(interaction, error, 'CommandName');
 * }
 * 
 * // Retry operation with exponential backoff
 * const result = await retryOperation(
 *     async () => await fetchFromAPI(),
 *     { maxAttempts: 3, initialDelay: 500 }
 * );
 * 
 * // Check if interaction is still valid
 * if (isInteractionValid(interaction)) {
 *     await interaction.reply({ content: 'Success!' });
 * }
 */

const ConsoleLogger = require('../utils/log/consoleLogger');
const { MessageFlags } = require('discord.js');

/**
 * Error categories for classification
 */
const ErrorCategory = {
    USER_ERROR: 'user_error',           // Invalid user input, permission denied
    DISCORD_API: 'discord_api',         // Discord API errors (rate limits, timeouts)
    EXTERNAL_API: 'external_api',       // External API failures (cat API, etc.)
    DATABASE: 'database',               // Database operation failures
    SYSTEM: 'system',                   // Internal system errors
    INTERACTION: 'interaction'          // Interaction-specific errors (token expiration, etc.)
};

/**
 * Standard error messages
 */
const ErrorMessages = {
    // User errors
    INVALID_INPUT: '❌ Invalid input. Please check your command and try again.',
    PERMISSION_DENIED: '❌ You don\'t have permission to perform this action.',
    RATE_LIMITED: '⏱️ Slow down! You\'re making requests too quickly. Please wait a moment.',
    
    // Discord API errors
    DISCORD_API_ERROR: '❌ Discord API error. Please try again in a moment.',
    INTERACTION_EXPIRED: '⚠️ This interaction has expired. Please run the command again.',
    INTERACTION_FAILED: '❌ Failed to respond to interaction. Please try again.',
    
    // External API errors
    EXTERNAL_API_UNAVAILABLE: '❌ External service is temporarily unavailable. Please try again later.',
    EXTERNAL_API_TIMEOUT: '⏱️ Request timed out. Please try again.',
    
    // Database errors
    DATABASE_ERROR: '❌ Database error occurred. Please try again.',
    
    // System errors
    SYSTEM_ERROR: '❌ An unexpected error occurred. Please contact support if this persists.',
    UNKNOWN_ERROR: '❌ An unknown error occurred. Please try again.'
};

/**
 * Categorize an error
 * @param {Error} error - The error to categorize
 * @returns {string} - Error category
 */
function categorizeError(error) {
    if (!error) return ErrorCategory.SYSTEM;
    
    // Discord API errors
    if (error.code >= 10000 && error.code < 100000) {
        return ErrorCategory.DISCORD_API;
    }
    
    // HTTP errors
    if (error.status) {
        if (error.status === 429) return ErrorCategory.DISCORD_API;
        if (error.status >= 400 && error.status < 500) return ErrorCategory.USER_ERROR;
        if (error.status >= 500) return ErrorCategory.EXTERNAL_API;
    }
    
    // Interaction errors
    if (error.message?.includes('interaction') || error.message?.includes('token')) {
        return ErrorCategory.INTERACTION;
    }
    
    // Database errors
    if (error.message?.includes('database') || error.message?.includes('SQLITE')) {
        return ErrorCategory.DATABASE;
    }
    
    return ErrorCategory.SYSTEM;
}

/**
 * Get user-friendly error message
 * @param {Error} error - The error
 * @param {string} category - Error category
 * @returns {string} - User-friendly message
 */
function getUserMessage(error, category) {
    // Check for specific error codes
    if (error.code === 10062) return ErrorMessages.INTERACTION_EXPIRED;
    if (error.code === 50013) return ErrorMessages.PERMISSION_DENIED;
    if (error.status === 429) return ErrorMessages.RATE_LIMITED;
    
    // Category-based messages
    switch (category) {
        case ErrorCategory.USER_ERROR:
            return ErrorMessages.INVALID_INPUT;
        case ErrorCategory.DISCORD_API:
            return ErrorMessages.DISCORD_API_ERROR;
        case ErrorCategory.EXTERNAL_API:
            return error.message?.includes('timeout') 
                ? ErrorMessages.EXTERNAL_API_TIMEOUT
                : ErrorMessages.EXTERNAL_API_UNAVAILABLE;
        case ErrorCategory.DATABASE:
            return ErrorMessages.DATABASE_ERROR;
        case ErrorCategory.INTERACTION:
            return ErrorMessages.INTERACTION_FAILED;
        case ErrorCategory.SYSTEM:
        default:
            return ErrorMessages.SYSTEM_ERROR;
    }
}

/**
 * Handle command error with automatic categorization and response
 * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
 * @param {Error} error - The error that occurred
 * @param {string} context - Context string for logging (e.g., command name)
 * @param {Object} options - Additional options
 * @param {boolean} options.silent - Don't send error message to user
 * @param {string} options.customMessage - Override default error message
 * @returns {Promise<boolean>} - Whether error was handled successfully
 */
async function handleCommandError(interaction, error, context, options = {}) {
    const { silent = false, customMessage = null } = options;
    
    // Categorize error
    const category = categorizeError(error);
    
    // Log error with context
    const logPrefix = context || 'Command';
    ConsoleLogger.error(logPrefix, `Error (${category}):`, error);
    
    // Send user message if not silent
    if (!silent) {
        const userMessage = customMessage || getUserMessage(error, category);
        
        try {
            // Check interaction state
            if (interaction.deferred && !interaction.replied) {
                // Interaction was deferred but not replied
                await interaction.editReply({ 
                    content: userMessage,
                    flags: MessageFlags.Ephemeral 
                }).catch(() => {});
                return true;
            } else if (!interaction.replied && !interaction.deferred) {
                // Can still reply
                await interaction.reply({ 
                    content: userMessage,
                    flags: MessageFlags.Ephemeral 
                }).catch(() => {});
                return true;
            } else if (interaction.replied) {
                // Already replied, try followUp
                await interaction.followUp({ 
                    content: userMessage,
                    flags: MessageFlags.Ephemeral 
                }).catch(() => {});
                return true;
            }
        } catch (e) {
            // Failed to send error message
            ConsoleLogger.error(logPrefix, 'Failed to send error message to user:', e);
            return false;
        }
    }
    
    return true;
}

/**
 * Retry an async operation with exponential backoff
 * @param {Function} operation - Async function to retry
 * @param {Object} options - Retry options
 * @param {number} options.maxAttempts - Maximum retry attempts (default: 3)
 * @param {number} options.initialDelay - Initial delay in ms (default: 500)
 * @param {number} options.maxDelay - Maximum delay in ms (default: 5000)
 * @param {Function} options.shouldRetry - Function to determine if error is retryable
 * @returns {Promise<any>} - Result of operation
 */
async function retryOperation(operation, options = {}) {
    const {
        maxAttempts = 3,
        initialDelay = 500,
        maxDelay = 5000,
        shouldRetry = () => true
    } = options;
    
    let lastError;
    
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
        try {
            return await operation();
        } catch (error) {
            lastError = error;
            
            // Don't retry if this is the last attempt
            if (attempt === maxAttempts) break;
            
            // Check if error is retryable
            if (!shouldRetry(error)) {
                throw error;
            }
            
            // Calculate backoff delay
            const delay = Math.min(initialDelay * Math.pow(2, attempt - 1), maxDelay);
            
            ConsoleLogger.warn('ErrorHandler', `Attempt ${attempt}/${maxAttempts} failed. Retrying in ${delay}ms...`);
            
            // Wait before retry
            await new Promise(resolve => setTimeout(resolve, delay));
        }
    }
    
    throw lastError;
}

/**
 * Check if interaction is still valid (not expired)
 * @param {import('discord.js').ChatInputCommandInteraction} interaction
 * @returns {boolean}
 */
function isInteractionValid(interaction) {
    if (!interaction) return false;
    
    // Interactions expire after 15 minutes
    const INTERACTION_LIFETIME_MS = 15 * 60 * 1000;
    const age = Date.now() - interaction.createdTimestamp;
    
    return age < INTERACTION_LIFETIME_MS;
}

/**
 * Safe interaction reply with fallback handling
 * @param {import('discord.js').ChatInputCommandInteraction} interaction
 * @param {Object} replyOptions - Reply options
 * @returns {Promise<boolean>} - Whether reply was successful
 */
async function safeReply(interaction, replyOptions) {
    try {
        // Check if interaction is still valid
        if (!isInteractionValid(interaction)) {
            ConsoleLogger.warn('ErrorHandler', 'Interaction expired, cannot reply');
            return false;
        }
        
        // Try appropriate reply method
        if (interaction.deferred && !interaction.replied) {
            await interaction.editReply(replyOptions);
        } else if (!interaction.replied && !interaction.deferred) {
            await interaction.reply(replyOptions);
        } else if (interaction.replied) {
            await interaction.followUp(replyOptions);
        } else {
            return false;
        }
        
        return true;
    } catch (error) {
        ConsoleLogger.error('ErrorHandler', 'Failed to send safe reply:', error);
        return false;
    }
}

module.exports = {
    ErrorCategory,
    ErrorMessages,
    categorizeError,
    getUserMessage,
    handleCommandError,
    retryOperation,
    isInteractionValid,
    safeReply
};
