// load .env
const remindersRepo = require('./db/repo/reminders');
const guildConfigRepo = require('./db/repo/guildConfig');

module.exports = {
    // Reminders
    addReminder: remindersRepo.addReminder,
    getReminders: remindersRepo.getReminders,
    deleteReminder: remindersRepo.deleteReminder,
    getAllPendingReminders: remindersRepo.getAllPendingReminders,
    getRemindersCount: remindersRepo.getRemindersCount,
    deleteAllReminders: remindersRepo.deleteAllReminders,

    // Configs
    setGuildConfig: guildConfigRepo.setGuildConfig,
    getGuildConfig: guildConfigRepo.getGuildConfig,
    getAllGuildConfigs: guildConfigRepo.getAllGuildConfigs
};
