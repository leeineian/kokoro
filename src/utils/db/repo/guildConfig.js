const db = require('../setup');
const ConsoleLogger = require('../../log/consoleLogger');

// Cache prepared statements
const stmt = {
    setConfig: db.prepare(`
        INSERT INTO guild_configs (guildId, config) VALUES (?, ?)
        ON CONFLICT(guildId) DO UPDATE SET config = excluded.config
    `),
    getConfig: db.prepare('SELECT config FROM guild_configs WHERE guildId = ?'),
    getAllConfigs: db.prepare('SELECT guildId, config FROM guild_configs')
};

module.exports = {
    setGuildConfig: (guildId, configObj) => {
        try {
            const json = JSON.stringify(configObj);
            stmt.setConfig.run(guildId, json);
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to set guild config:', error);
            throw new Error('Database operation failed: setGuildConfig');
        }
    },

    getGuildConfig: (guildId) => {
        try {
            const row = stmt.getConfig.get(guildId);
            return row ? JSON.parse(row.config) : null;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to get guild config:', error);
            return null;
        }
    },

    getAllGuildConfigs: () => {
        try {
            const rows = stmt.getAllConfigs.all();
            const configs = {};
            for (const row of rows) {
                configs[row.guildId] = JSON.parse(row.config);
            }
            return configs;
        } catch (error) {
            ConsoleLogger.error('Database', 'Failed to get all guild configs:', error);
            return {};
        }
    }
};

