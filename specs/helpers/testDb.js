const db = require('../../src/utils/database');

/**
 * Test Database Utilities
 * Provides helpers for test isolation and cleanup
 */

/**
 * Clean up all test data from the database
 */
function cleanupTestData() {
    // Clean reminders with test user IDs
    const testUserIds = ['test_user', 'user1', 'user123', 'bulk_test', 'count_test', 'getall_test'];
    testUserIds.forEach(userId => {
        try {
            db.deleteAllReminders(userId);
        } catch (err) {
            // Ignore errors during cleanup
        }
    });

    // Clean AI prompts for test users
    testUserIds.forEach(userId => {
        try {
            db.aiPrompts?.deletePrompt?.(userId);
        } catch (err) {
            // Ignore errors during cleanup
        }
    });

    // Clean webhook configs for test guilds
    const testGuildIds = ['test_guild', '987654321', 'guild123'];
    testGuildIds.forEach(guildId => {
        try {
            db.webhookConfig?.deleteAll?.(guildId);
        } catch (err) {
            // Ignore errors during cleanup
        }
    });
}

/**
 * Seed test reminders for a user
 */
function seedTestReminders(userId, count = 5) {
    const ids = [];
    const baseTime = Date.now() + 60000; // 1 minute from now

    for (let i = 0; i < count; i++) {
        const id = db.addReminder(
            userId,
            `channel${i}`,
            `Test reminder ${i + 1}`,
            baseTime + (i * 10000) // Stagger by 10 seconds
        );
        ids.push(id);
    }

    return ids;
}

/**
 * Seed past-due reminders for testing delivery
 */
function seedPastDueReminders(userId, count = 3) {
    const ids = [];
    const baseTime = Date.now() - 60000; // 1 minute ago

    for (let i = 0; i < count; i++) {
        const id = db.addReminder(
            userId,
            `channel${i}`,
            `Past due reminder ${i + 1}`,
            baseTime - (i * 10000)
        );
        ids.push(id);
    }

    return ids;
}

/**
 * Create a test AI prompt
 */
function seedTestAiPrompt(userId, prompt = 'You are a test bot') {
    if (db.aiPrompts?.setPrompt) {
        db.aiPrompts.setPrompt(userId, prompt);
        return prompt;
    }
    return null;
}

/**
 * Create a test webhook config
 */
function seedTestWebhookConfig(guildId, categoryId, channels = []) {
    if (db.webhookConfig?.set) {
        const config = {
            categoryId,
            channels: channels.map(ch => ({
                id: ch.id,
                name: ch.name,
                webhookId: `webhook_${ch.id}`,
                webhookToken: `token_${ch.id}`
            }))
        };
        db.webhookConfig.set(guildId, categoryId, config);
        return config;
    }
    return null;
}

/**
 * Get all pending test reminders
 */
function getTestReminders(userId) {
    return db.getReminders(userId);
}

/**
 * Verify reminder was marked as sent
 */
function isReminderSent(reminderId, userId) {
    const reminders = db.getReminders(userId);
    const reminder = reminders.find(r => r.id === reminderId);
    return reminder ? !!reminder.sent_at : false;
}

/**
 * Setup - run before all tests
 */
function setupTestDatabase() {
    cleanupTestData();
}

/**
 * Teardown - run after all tests
 */
function teardownTestDatabase() {
    cleanupTestData();
}

/**
 * Isolate each test
 */
function beforeEachTest() {
    // Optional: can be used for transaction-based isolation in the future
}

function afterEachTest() {
    // Optional: can be used for transaction rollback in the future
}

module.exports = {
    cleanupTestData,
    seedTestReminders,
    seedPastDueReminders,
    seedTestAiPrompt,
    seedTestWebhookConfig,
    getTestReminders,
    isReminderSent,
    setupTestDatabase,
    teardownTestDatabase,
    beforeEachTest,
    afterEachTest
};
