const db = require('../client');

module.exports = {
    setGuildConfig: (guildId, configObj) => {
        const json = JSON.stringify(configObj);
        db.prepare(`
            INSERT INTO guild_configs (guildId, config) VALUES (?, ?)
            ON CONFLICT(guildId) DO UPDATE SET config = excluded.config
        `).run(guildId, json);
    },

    getGuildConfig: (guildId) => {
        const row = db.prepare('SELECT config FROM guild_configs WHERE guildId = ?').get(guildId);
        return row ? JSON.parse(row.config) : null;
    },

    getAllGuildConfigs: () => {
        const rows = db.prepare('SELECT guildId, config FROM guild_configs').all();
        const configs = {};
        for (const row of rows) {
            configs[row.guildId] = JSON.parse(row.config);
        }
        return configs;
    }
};
