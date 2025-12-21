// load .env
const remindersRepo = require('./db/repo/reminders');
const guildConfigRepo = require('./db/repo/guildConfig');

/**
 * Database Facade
 * Centralizes access to all database repositories.
 * 
 * @module utils/database
 */
module.exports = {
    // --- Reminders ---
    /** @type {import('./db/repo/reminders').addReminder} */
    addReminder: remindersRepo.addReminder,
    /** @type {import('./db/repo/reminders').getReminders} */
    getReminders: remindersRepo.getReminders,
    /** @type {import('./db/repo/reminders').deleteReminder} */
    deleteReminder: remindersRepo.deleteReminder,
    /** @type {import('./db/repo/reminders').getAllPendingReminders} */
    getAllPendingReminders: remindersRepo.getAllPendingReminders,
    /** @type {import('./db/repo/reminders').getRemindersCount} */
    getRemindersCount: remindersRepo.getRemindersCount,
    /** @type {import('./db/repo/reminders').deleteAllReminders} */
    deleteAllReminders: remindersRepo.deleteAllReminders,

    // --- Guild Configs ---
    /** @type {import('./db/repo/guildConfig').setGuildConfig} */
    setGuildConfig: guildConfigRepo.setGuildConfig,
    /** @type {import('./db/repo/guildConfig').getGuildConfig} */
    getGuildConfig: guildConfigRepo.getGuildConfig,
    /** @type {import('./db/repo/guildConfig').getAllGuildConfigs} */
    getAllGuildConfigs: guildConfigRepo.getAllGuildConfigs,

    // --- Webhooks ---
    webhookConfig: require('./db/repo/webhookConfig'),

    // --- AI ---
    aiPrompts: require('./db/repo/aiPrompts'),
    aiMemory: require('./db/repo/aiMemory')
};
