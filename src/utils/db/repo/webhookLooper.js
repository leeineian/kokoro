const db = require('../setup');
const ConsoleLogger = require('../../log/consoleLogger');

// Cache prepared statements
const stmt = {
    add: db.prepare(`
        INSERT OR REPLACE INTO loop_channels (
            channelId, channelName, channelType, rounds, interval,
            activeChannelName, inactiveChannelName, message, isRunning
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT isRunning FROM loop_channels WHERE channelId = ?), 0))
    `),
    get: db.prepare('SELECT * FROM loop_channels WHERE channelId = ?'),
    getAll: db.prepare('SELECT * FROM loop_channels'),
    delete: db.prepare('DELETE FROM loop_channels WHERE channelId = ?'),
    clearAll: db.prepare('DELETE FROM loop_channels'),
    setState: db.prepare('UPDATE loop_channels SET isRunning = ? WHERE channelId = ?'),
    setName: db.prepare('UPDATE loop_channels SET channelName = ? WHERE channelId = ?')
};

/**
 * Add or update a loop channel configuration
 * @param {string} channelId - Channel or category ID
 * @param {Object} config - Configuration object
 * @param {string} config.channelName - Channel/category name
 * @param {string} config.channelType - Type: 'category' or 'channel'
 * @param {number} config.rounds - Number of rounds (0 for random)
 * @param {number} config.interval - Interval in milliseconds (0 for infinite)
 * @param {string} [config.activeChannelName] - Name when loop is active
 * @param {string} [config.inactiveChannelName] - Name when loop is inactive
 * @param {string} [config.message] - Message to send (defaults to @everyone)
 */
const addLoopConfig = (channelId, config) => {
    try {
        return stmt.add.run(
            channelId,
            config.channelName,
            config.channelType,
            config.rounds,
            config.interval,
            config.activeChannelName || null,
            config.inactiveChannelName || null,
            config.message || '@everyone',
            channelId // For subquery to preserve state
        );
    } catch (error) {
        ConsoleLogger.error('Database', 'Failed to add loop config:', error);
        throw error;
    }
};

/**
 * Get a specific loop channel configuration
 * @param {string} channelId - Channel or category ID
 * @returns {Object|null} Configuration object or null if not found
 */
const getLoopConfig = (channelId) => {
    try {
        return stmt.get.get(channelId);
    } catch (error) {
        ConsoleLogger.error('Database', 'Failed to get loop config:', error);
        return null;
    }
};

/**
 * Get all loop channel configurations
 * @returns {Array<Object>} Array of configuration objects
 */
const getAllLoopConfigs = () => {
    try {
        return stmt.getAll.all();
    } catch (error) {
        ConsoleLogger.error('Database', 'Failed to get all loop configs:', error);
        return [];
    }
};

/**
 * Delete a specific loop channel configuration
 * @param {string} channelId - Channel or category ID
 */
const deleteLoopConfig = (channelId) => {
    try {
        return stmt.delete.run(channelId);
    } catch (error) {
        ConsoleLogger.error('Database', 'Failed to delete loop config:', error);
        throw error;
    }
};

/**
 * Clear all loop channel configurations
 */
const clearAllLoopConfigs = () => {
    try {
        return stmt.clearAll.run();
    } catch (error) {
        ConsoleLogger.error('Database', 'Failed to clear all loop configs:', error);
        throw error;
    }
};

/**
 * Set the running state of a loop
 * @param {string} channelId 
 * @param {boolean} isRunning 
 */
const setLoopState = (channelId, isRunning) => {
    try {
        return stmt.setState.run(isRunning ? 1 : 0, channelId);
    } catch (error) {
        ConsoleLogger.error('Database', 'Failed to set loop state:', error);
        throw error;
    }
};

/**
 * Update the stored channel name
 * @param {string} channelId 
 * @param {string} channelName 
 */
const updateChannelName = (channelId, channelName) => {
    try {
        return stmt.setName.run(channelName, channelId);
    } catch (error) {
        ConsoleLogger.error('Database', 'Failed to update channel name:', error);
        throw error;
    }
};

module.exports = {
    addLoopConfig,
    getLoopConfig,
    getAllLoopConfigs,
    deleteLoopConfig,
    clearAllLoopConfigs,
    setLoopState,
    updateChannelName
};
