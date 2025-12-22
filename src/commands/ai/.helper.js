/**
 * AI Command Helpers
 * Shared constants and validation utilities for AI commands and daemons
 */

// Default personality for the AI
const DEFAULT_SYSTEM_PROMPT = "Respond directly and concisely. Do not narrate the conversation, summarize the chat history, or comment on who is speaking unless explicitly asked. Stay in character as a direct participant.";

// Max length for custom system prompts
const MAX_PROMPT_LENGTH = 1000;

// Rate limiting constants
const RATE_LIMITS = {
    PROMPT_UPDATE: { maxRequests: 5, windowMs: 60000 }, // 5 updates per minute
    MEMORY_RESET: { maxRequests: 3, windowMs: 300000 }  // 3 resets per 5 minutes
};

// Forbidden patterns in prompts (potential jailbreak/injection attempts)
const FORBIDDEN_PROMPT_PATTERNS = [
    /ignore\s+(previous|all)\s+(instructions|rules)/gi,
    /disregard\s+(previous|all)\s+(instructions|rules)/gi,
    /you\s+are\s+now/gi,
    /new\s+instructions?/gi,
    /system\s*:\s*/gi,
    /assistant\s*:\s*/gi,
    /<\|.*?\|>/g, // Special tokens
    /\[INST\]/gi,
    /\[\/INST\]/gi
];

/**
 * Sanitizes prompt text by removing potential injection attempts
 * @param {string} text - Raw prompt text
 * @returns {string} - Sanitized prompt text
 */
function sanitizePrompt(text) {
    if (typeof text !== 'string') return '';
    
    // Trim excessive whitespace
    let sanitized = text.trim().replace(/\s+/g, ' ');
    
    // Remove control characters except newlines
    sanitized = sanitized.replace(/[\x00-\x09\x0B-\x1F\x7F-\x9F]/g, '');
    
    return sanitized;
}

/**
 * Validates prompt doesn't contain forbidden patterns
 * @param {string} text - Prompt text to validate
 * @returns {boolean} - True if valid
 */
function validatePromptSafety(text) {
    if (typeof text !== 'string') return false;
    
    // Check against forbidden patterns
    return !FORBIDDEN_PROMPT_PATTERNS.some(pattern => pattern.test(text));
}

module.exports = {
    DEFAULT_SYSTEM_PROMPT,
    MAX_PROMPT_LENGTH,
    RATE_LIMITS,
    FORBIDDEN_PROMPT_PATTERNS,
    sanitizePrompt,
    validatePromptSafety
};
