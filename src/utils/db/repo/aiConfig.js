const db = require('../setup');
const ConsoleLogger = require('../../log/consoleLogger');

// Cache prepared statements
const stmt = {
    // AI Prompts
    getPrompt: db.prepare('SELECT prompt FROM user_ai_prompts WHERE userId = ?'),
    setPrompt: db.prepare('INSERT OR REPLACE INTO user_ai_prompts (userId, prompt) VALUES (?, ?)'),
    deletePrompt: db.prepare('DELETE FROM user_ai_prompts WHERE userId = ?'),
    
    // AI Memory
    getBarrier: db.prepare('SELECT timestamp FROM ai_memory_barriers WHERE channelId = ?'),
    setBarrier: db.prepare('INSERT OR REPLACE INTO ai_memory_barriers (channelId, timestamp) VALUES (?, ?)')
};

module.exports = {
    // ========================================================================
    // AI PROMPTS
    // ========================================================================
    
    /**
     * Get a user's custom prompt. Returns undefined if not found.
     */
    getPrompt: (userId) => {
        try {
            const row = stmt.getPrompt.get(userId);
            return row ? row.prompt : undefined;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to get AI prompt:', error);
            return undefined;
        }
    },

    /**
     * Set (upsert) a user's custom prompt.
     */
    setPrompt: (userId, prompt) => {
        try {
            const info = stmt.setPrompt.run(userId, prompt);
            return info.changes;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to set AI prompt:', error);
            return 0;
        }
    },

    /**
     * Delete a user's custom prompt (reset to default).
     */
    deletePrompt: (userId) => {
        try {
            const info = stmt.deletePrompt.run(userId);
            return info.changes;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to delete AI prompt:', error);
            return 0;
        }
    },

    // ========================================================================
    // AI MEMORY
    // ========================================================================
    
    /**
     * Get the timestamp of the last memory reset for this channel.
     * Returns 0 if never reset.
     */
    getBarrier: (channelId) => {
        try {
            const row = stmt.getBarrier.get(channelId);
            return row ? row.timestamp : 0;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to get AI memory barrier:', error);
            return 0;
        }
    },

    /**
     * Set a new barrier for this channel to "now".
     * AI will effectively ignore messages before this point.
     */
    resetMemory: (channelId) => {
        try {
            const now = Date.now();
            stmt.setBarrier.run(channelId, now);
            return now;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to reset AI memory:', error);
            return 0;
        }
    }
};
