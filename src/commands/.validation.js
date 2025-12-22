/**
 * Command Validation Utilities
 * Centralized validation, sanitization, and security utilities for all commands
 */

// Rate limiting store (in-memory, per-process)
const rateLimitStore = new Map();

/**
 * Sanitizes user input by removing potentially dangerous characters
 * @param {string} input - Raw user input
 * @param {Object} options - Sanitization options
 * @param {boolean} options.allowNewlines - Allow newline characters
 * @param {boolean} options.allowAnsi - Allow ANSI escape codes
 * @returns {string} - Sanitized input
 */
function sanitizeInput(input, options = {}) {
    if (typeof input !== 'string') return '';
    
    let sanitized = input;
    
    // Remove control characters (except newlines if allowed)
    if (options.allowNewlines) {
        sanitized = sanitized.replace(/[\x00-\x09\x0B-\x1F\x7F-\x9F]/g, '');
    } else {
        sanitized = sanitized.replace(/[\x00-\x1F\x7F-\x9F]/g, '');
    }
    
    // Remove ANSI codes unless explicitly allowed
    if (!options.allowAnsi) {
        sanitized = sanitized.replace(/\x1B\[[0-9;]*[a-zA-Z]/g, '');
    }
    
    // Remove zero-width characters (potential for steganography/confusion)
    sanitized = sanitized.replace(/[\u200B-\u200D\uFEFF]/g, '');
    
    return sanitized;
}

/**
 * Validates string length is within bounds
 * @param {string} str - String to validate
 * @param {number} min - Minimum length (inclusive)
 * @param {number} max - Maximum length (inclusive)
 * @returns {boolean} - True if valid
 */
function validateLength(str, min, max) {
    if (typeof str !== 'string') return false;
    const len = str.length;
    return len >= min && len <= max;
}

/**
 * Validates string doesn't contain forbidden patterns
 * @param {string} str - String to validate
 * @param {RegExp[]} patterns - Array of forbidden regex patterns
 * @returns {boolean} - True if valid (no matches)
 */
function validateNoForbiddenPatterns(str, patterns) {
    if (typeof str !== 'string') return false;
    return !patterns.some(pattern => pattern.test(str));
}

/**
 * Validates string is proper UTF-8 and doesn't contain malformed sequences
 * @param {string} str - String to validate
 * @returns {boolean} - True if valid
 */
function validateEncoding(str) {
    if (typeof str !== 'string') return false;
    
    try {
        // Check for unpaired surrogates (invalid UTF-16)
        const encoded = new TextEncoder().encode(str);
        const decoded = new TextDecoder('utf-8', { fatal: true }).decode(encoded);
        return decoded === str;
    } catch (e) {
        return false;
    }
}

/**
 * Validates color choice against allowed values
 * @param {string} color - Color to validate
 * @param {string[]} allowedColors - Array of allowed color names
 * @returns {boolean} - True if valid
 */
function validateColor(color, allowedColors) {
    if (!color) return true; // null/undefined is valid (means no color)
    return allowedColors.includes(color);
}

/**
 * Rate limiting check
 * @param {string} userId - User ID to check
 * @param {string} action - Action being rate limited
 * @param {number} maxRequests - Maximum requests allowed
 * @param {number} windowMs - Time window in milliseconds
 * @returns {boolean} - True if allowed, false if rate limited
 */
function checkRateLimit(userId, action, maxRequests, windowMs) {
    const key = `${userId}:${action}`;
    const now = Date.now();
    
    if (!rateLimitStore.has(key)) {
        rateLimitStore.set(key, []);
    }
    
    const timestamps = rateLimitStore.get(key);
    
    // Remove expired timestamps
    const validTimestamps = timestamps.filter(ts => now - ts < windowMs);
    
    // Check if limit exceeded
    if (validTimestamps.length >= maxRequests) {
        return false;
    }
    
    // Add new timestamp
    validTimestamps.push(now);
    rateLimitStore.set(key, validTimestamps);
    
    return true;
}

/**
 * Clears rate limit for a user/action (useful for testing or admin override)
 * @param {string} userId - User ID
 * @param {string} action - Action to clear
 */
function clearRateLimit(userId, action) {
    const key = `${userId}:${action}`;
    rateLimitStore.delete(key);
}

/**
 * Validates URL is safe and points to expected domain
 * @param {string} url - URL to validate
 * @param {string[]} allowedDomains - Array of allowed domain patterns
 * @returns {boolean} - True if valid
 */
function validateUrl(url, allowedDomains) {
    if (typeof url !== 'string') return false;
    
    try {
        const parsed = new URL(url);
        
        // Must be HTTP or HTTPS
        if (!['http:', 'https:'].includes(parsed.protocol)) {
            return false;
        }
        
        // Check against allowed domains
        return allowedDomains.some(domain => {
            if (domain.startsWith('*.')) {
                // Wildcard subdomain
                const baseDomain = domain.slice(2);
                return parsed.hostname === baseDomain || parsed.hostname.endsWith('.' + baseDomain);
            }
            return parsed.hostname === domain;
        });
    } catch (e) {
        return false;
    }
}

/**
 * Validates timestamp is in reasonable future range
 * @param {number} timestamp - Unix timestamp in milliseconds
 * @param {number} maxFutureMs - Maximum future time allowed
 * @returns {boolean} - True if valid
 */
function validateFutureTimestamp(timestamp, maxFutureMs) {
    if (typeof timestamp !== 'number') return false;
    const now = Date.now();
    const maxFuture = now + maxFutureMs;
    return timestamp > now && timestamp <= maxFuture;
}

/**
 * Validates timestamp is not too far in the past
 * @param {number} timestamp - Unix timestamp in milliseconds
 * @param {number} minPastMs - Minimum past time allowed (negative values = past)
 * @returns {boolean} - True if valid
 */
function validateMinimumInterval(timestamp, minPastMs) {
    if (typeof timestamp !== 'number') return false;
    const now = Date.now();
    return timestamp >= now + minPastMs;
}

// Forbidden patterns for text input (potential injection/abuse)
const FORBIDDEN_PATTERNS = {
    SCRIPT_TAGS: /<script[^>]*>.*?<\/script>/gi,
    HTML_TAGS: /<[^>]+>/g,
    DISCORD_TOKEN: /[MN][A-Za-z\d]{23}\.[\w-]{6}\.[\w-]{27}/g,
    EXCESSIVE_MENTIONS: /(@(everyone|here)){2,}/gi,
    URL_SCHEMES: /^(javascript|data|vbscript):/i
};

// Allowed colors for cat commands
const ALLOWED_COLORS = ['gray', 'red', 'green', 'yellow', 'blue', 'pink', 'cyan', 'white'];

module.exports = {
    // Core validation functions
    sanitizeInput,
    validateLength,
    validateNoForbiddenPatterns,
    validateEncoding,
    validateColor,
    validateUrl,
    validateFutureTimestamp,
    validateMinimumInterval,
    
    // Rate limiting
    checkRateLimit,
    clearRateLimit,
    
    // Constants
    FORBIDDEN_PATTERNS,
    ALLOWED_COLORS
};
