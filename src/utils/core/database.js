// Database Facade - Central access point for all database operations
const remindersRepo = require('../db/repo/reminderConfig');
const guildConfigRepo = require('../db/repo/guildConfig');

/**
 * Database Facade
 * Provides a unified interface to all database repositories
 */
const db = {
    // ========================================================================
    // REMINDERS
    // ========================================================================
    /** @type {import('../db/repo/reminderConfig').addReminder} */
    addReminder: remindersRepo.addReminder,
    /** @type {import('../db/repo/reminderConfig').getReminders} */
    getReminders: remindersRepo.getReminders,
    /** @type {import('../db/repo/reminderConfig').deleteReminder} */
    deleteReminder: remindersRepo.deleteReminder,
    /** @type {import('../db/repo/reminderConfig').getAllPendingReminders} */
    getAllPendingReminders: remindersRepo.getAllPendingReminders,
    /** @type {import('../db/repo/reminderConfig').getRemindersCount} */
    getRemindersCount: remindersRepo.getRemindersCount,
    /** @type {import('../db/repo/reminderConfig').deleteAllReminders} */
    deleteAllReminders: remindersRepo.deleteAllReminders,
    /** @type {import('../db/repo/reminderConfig').markReminderAsSent} */
    markReminderAsSent: remindersRepo.markReminderAsSent,
    /** @type {import('../db/repo/reminderConfig').resetReminderSentStatus} */
    resetReminderSentStatus: remindersRepo.resetReminderSentStatus,
    /** @type {import('../db/repo/reminderConfig').resetAllReminderLocks} */
    resetAllReminderLocks: remindersRepo.resetAllReminderLocks,

    // ========================================================================
    // GUILD CONFIG (Audit Logging)
    // ========================================================================
    /** @type {import('../db/repo/guildConfig').setGuildConfig} */
    setGuildConfig: guildConfigRepo.setGuildConfig,
    /** @type {import('../db/repo/guildConfig').getGuildConfig} */
    getGuildConfig: guildConfigRepo.getGuildConfig,
    /** @type {import('../db/repo/guildConfig').getAllGuildConfigs} */
    getAllGuildConfigs: guildConfigRepo.getAllGuildConfigs,

    // ========================================================================
    // REPOSITORIES (Direct Access)
    // ========================================================================
    webhookLooper: require('../db/repo/webhookLooper'),

    // AI Configuration (prompts + memory)
    ai: require('../db/repo/aiConfig'),
    
    // Legacy aliases for backward compatibility
    aiPrompts: require('../db/repo/aiConfig'),
    aiMemory: require('../db/repo/aiConfig')
};

module.exports = db;
