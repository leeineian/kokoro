const { PermissionFlagsBits, ChannelType, MessageFlags } = require('discord.js');
const ConsoleLogger = require('../utils/consoleLogger');
const V2Builder = require('../utils/components');

// --- STATE ---
// Map<categoryId, { name: string, hooks: Array<{hook, channelName}> }>
const configuredCategories = new Map();
// Map<categoryId, { stop: Function, roundsTotal: number, currentRound: number }>
const activePingLoops = new Map();

// --- CONSTANTS ---
const WEBHOOK_NAME = 'DebugPingHook';
const LOOP_DELAY_MS = 2000; // Delay between loop iterations

// --- HELPERS ---
const sleep = (ms) => new Promise(resolve => setTimeout(resolve, ms));

/**
 * Lists configured and active categories.
 */
async function listPingCategories(interaction) {
    if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
        return interaction.reply({ content: '❌ **Access Denied.** Admin only.', flags: MessageFlags.Ephemeral });
    }

    if (configuredCategories.size === 0) {
        return interaction.reply({ content: 'ℹ️ No categories are currently configured.', flags: MessageFlags.Ephemeral });
    }

    const list = Array.from(configuredCategories.entries())
        .map(([id, data]) => {
            const active = activePingLoops.get(id);
            let status;
            if (active) {
                status = `🟢 **Running** (Round ${active.currentRound}/${active.roundsTotal})`;
            } else if (data.hooks === null) {
                status = `⚪ **Configured** (Lazy Loaded)`;
            } else {
                status = `🟠 **Configured** (Ready)`;
            }
            return `- **${data.name}** [${data.hooks ? data.hooks.length : '?'} channels] - ${status}`;
        })
        .join('\n');

    return interaction.reply({ content: `**Ping Categories:**\n${list}`, flags: MessageFlags.Ephemeral });
}

/**
 * Stops all loops and clears configuration.
 */
async function resetPingCategories(interaction) {
    if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
        return interaction.reply({ content: '❌ **Access Denied.** Admin only.', flags: MessageFlags.Ephemeral });
    }

    const activeCount = activePingLoops.size;
    const configCount = configuredCategories.size;

    // Stop loops
    for (const [id, loop] of activePingLoops) {
        loop.stop();
    }
    activePingLoops.clear();
    configuredCategories.clear();

    // Clear DB
    try {
        webhookConfig.clearAllRawCategories();
    } catch (err) {
        ConsoleLogger.error('WebhookPinger', 'DB Reset Error:', err);
    }

    ConsoleLogger.warn('WebhookPinger', `Reset by ${interaction.user.tag}. Stopped ${activeCount} loops, cleared ${configCount} configurations.`);

    return interaction.reply({ content: `✅ **Reset Complete.**\nStopped ${activeCount} active loops.\nCleared ${configCount} configured categories.`, flags: MessageFlags.Ephemeral });
}

/**
 * PHASE 1: SETUP
 * Configures a category: validates channels and prepares webhooks.
 */
async function setPingCategory(interaction) {
    if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
        return interaction.reply({ content: '❌ **Access Denied.** Admin only.', flags: MessageFlags.Ephemeral });
    }

    const category = interaction.options.getChannel('category');

    if (!category || category.type !== ChannelType.GuildCategory) {
        return interaction.reply({ content: '❌ Invalid input. Please select a valid Category.', flags: MessageFlags.Ephemeral });
    }

    if (activePingLoops.has(category.id)) {
        return interaction.reply({ content: `⚠️ Category **${category.name}** is currently running! Reset first to make changes.`, flags: MessageFlags.Ephemeral });
    }

    await interaction.deferReply();
    const message = await interaction.fetchReply(); // FETCH PERSISTENT MESSAGE (Fixes 15m timeout)

    // Save to DB
    try {
        webhookConfig.addRawCategory(category.id, category.name);
    } catch (err) {
        ConsoleLogger.error('WebhookPinger', 'DB Error:', err);
    }

    // Log Buffer for UI
    const logs = [];
    const updateInterval = 2000;
    let lastUpdate = 0;

    const updateUI = async (final = false) => {
        const now = Date.now();
        if (!final && now - lastUpdate < updateInterval) return;
        lastUpdate = now;

        const logContent = logs.slice(-15).join('\n'); // Show last 15 lines
        const container = V2Builder.container([
            V2Builder.textDisplay(`Processing **${category.name}**...`),
            V2Builder.textDisplay(`\`\`\`log\n${logContent}\n\`\`\``)
        ]);

        // Use message.edit()
        await message.edit({ 
            components: [container],
            flags: MessageFlags.IsComponentsV2
        }).catch(err => ConsoleLogger.error('WebhookPinger', 'UI Update Error:', err));
    };

    const stripAnsi = (str) => str.replace(/\x1B\[\d+m/g, '');

    const logCallback = (msg) => {
        logs.push(stripAnsi(msg));
        updateUI();
    };

    let hooks;
    try {
        hooks = await prepareWebhooksForCategory(category, interaction.client, logCallback);
        await updateUI(true); // Final update
    } catch (error) {
        ConsoleLogger.error(`Setup-${category.name}`, `Error:`, error);
        return message.edit(`❌ Error setting up webhooks: ${error.message}`);
    }

    // Save Configuration
    configuredCategories.set(category.id, {
        name: category.name,
        hooks: hooks
    });

    const successContainer = V2Builder.container([
        V2Builder.textDisplay(`✅ **Category Configured**\n> **${category.name}**\n> Prepared ${hooks.length} webhooks.\n> Run \`/debug webhook-pinger run\` to start.`)
    ]);

    return message.edit({ 
        components: [successContainer],
        flags: MessageFlags.IsComponentsV2
    });
}

/**
 * Helper to fetch/create webhooks for a category.
 */
async function prepareWebhooksForCategory(category, client, logCallback) {
    const targetChannels = category.children.cache.filter(c => c.type === ChannelType.GuildText);
    if (targetChannels.size === 0) throw new Error('No text channels found.');

    const hooks = [];
    
    let i = 0;
    for (const [_, channel] of targetChannels) {
        let attempts = 0;
        let success = false;
        
        while (!success && attempts < 3) {
            attempts++;
            try {
                if (attempts === 1) { // Only log increment once
                    i++;
                    const msg = `[Setup-${category.name}] Processing channel ${i}/${targetChannels.size}: ${channel.name}`;
                    ConsoleLogger.debug('Setup', msg);
                    if (logCallback) logCallback(msg);
                }

                const existingHooks = await channel.fetchWebhooks();
                // STRICT CHECK: Only use OUR hooks
                let hook = existingHooks.find(h => h.owner.id === client.user.id && h.name === WEBHOOK_NAME);

                if (!hook) {
                    if (existingHooks.size >= 10) {
                        // SAFETY FIX: Do NOT use random hooks. Skip if full.
                        const warning = `[Setup-${category.name}] ⚠️ Channel '${channel.name}' has 10 webhooks and none belong to us. Skipping.`;
                        ConsoleLogger.warn('Setup', warning);
                        if (logCallback) logCallback(warning);
                        success = true; // Treated as "done" (skipped)
                        continue; 
                    } else {
                        hook = await channel.createWebhook({
                            name: WEBHOOK_NAME,
                            avatar: category.guild.iconURL(), 
                        });
                    }
                }
                if (hook) hooks.push({ hook, channelName: channel.name });
                success = true;
            } catch (err) {
                // Rate Limit Handling
                if (err.status === 429 || err.code === 429) {
                    // Smart Retry-After
                    let retryTime = 5000;
                    if (err.retry_after) retryTime = err.retry_after * 1000; // v13 style
                    if (err.global) retryTime += 1000; // Add buffer for global
                    
                    ConsoleLogger.warn('Setup', `[Setup-${category.name}] ⚠️ Rate Limit on '${channel.name}'. Waiting ${(retryTime/1000).toFixed(1)}s (Attempt ${attempts}/3)...`);
                    if (logCallback) logCallback(`[Setup-${category.name}] ⚠️ Rate Limit. Retrying in ${(retryTime/1000).toFixed(1)}s...`);
                    
                    await sleep(retryTime);
                    continue; // Retry
                }

                const errName = err.code === 50013 ? 'Missing Permissions' : err.message;
                const errorMsg = `[Setup-${category.name}] ❌ Failed to setup channel '${channel.name}': ${errName}. Skipping.`;
                ConsoleLogger.error('Setup', errorMsg);
                if (logCallback) logCallback(errorMsg);
                success = true; // Stop retrying on non-transient errors
            }
        }
    }
    return hooks;
}

/**
 * PHASE 2: EXECUTION
 * Starts the ping loop for ALL configured categories.
 */
async function runPingCategories(interaction) {
    if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
        return interaction.reply({ content: '❌ **Access Denied.** Admin only.', flags: MessageFlags.Ephemeral });
    }

    if (configuredCategories.size === 0) {
        return interaction.reply({ content: '❌ No categories configured! Use \`/debug webhook-pinger set\` first.', flags: MessageFlags.Ephemeral });
    }

    if (activePingLoops.size > 0) {
        return interaction.reply({ content: '⚠️ Pings are already running! Wait for them to finish or reset.', flags: MessageFlags.Ephemeral });
    }

    const rounds = interaction.options.getInteger('rounds') || 1; // Default 1 just in case, but command should enforce required

    await interaction.deferReply();
    const message = await interaction.fetchReply(); // FETCH PERSISTENT MESSAGE

    // Log Buffer for UI
    const logs = [];
    const updateInterval = 2000;
    let lastUpdate = 0;

    const updateUI = async (force = false) => {
        const now = Date.now();
        if (!force && now - lastUpdate < updateInterval) return;
        lastUpdate = now;

        const logContent = logs.join('\n'); // Show all (capped by push logic)
        const container = V2Builder.container([
            V2Builder.textDisplay(`\`\`\`log\n${logContent}\n\`\`\``)
        ]);

        // Use message.edit() which never expires
        await message.edit({ 
            components: [container],
            flags: MessageFlags.IsComponentsV2
        }).catch(err => ConsoleLogger.error('WebhookPinger', 'UI Update Error:', err));
    };

    const stripAnsi = (str) => str.replace(/\x1B\[\d+m/g, '');

    const logCallback = (msg, force = false) => {
        if (logs.length >= 50) logs.shift(); // MEMORY LEAK FIX: Cap at 50 lines
        logs.push(stripAnsi(msg));
        updateUI(force);
    };

    let totalChannels = 0;
    for (const cfg of configuredCategories.values()) {
        if (cfg.hooks) totalChannels += cfg.hooks.length;
    }
    const catCount = configuredCategories.size;
    const rStr = rounds === 1 ? 'Round' : 'Rounds';
    const cStr = catCount === 1 ? 'Category' : 'Categories';
    const chStr = totalChannels === 1 ? 'Channel' : 'Channels';

    const startMsg = `🚀 ${rStr}: ${rounds} | ${cStr}: ${catCount} | ${chStr}: ${totalChannels}`;
    ConsoleLogger.success('WebhookPinger', startMsg);
    logs.push(startMsg);
    await updateUI(true);

    const startTime = Date.now();
    const categoriesToRun = Array.from(configuredCategories.entries());

    const executionState = { isInitializing: true };

    for (const [id, config] of categoriesToRun) {
        // ZOMBIE CHECK: If reset happened during startup, stop.
        if (!configuredCategories.has(id)) continue;

        // Lazy Load check
        if (!config.hooks) {
            ConsoleLogger.info(config.name, `Lazy loading webhooks...`);
            logCallback(`[${config.name}] Lazy loading webhooks...`, true);
            try {
                const category = await interaction.client.channels.fetch(id).catch(() => null);
                if (!category) {
                    ConsoleLogger.warn(config.name, `Category not found! Automatically removing from config.`);
                    logCallback(`[${config.name}] Category not found! Auto-removing...`, true);
                    
                    // Cleanup
                    configuredCategories.delete(id);
                    webhookConfig.deleteRawCategory(id);
                    continue;
                }

                // Sync Name if changed
                if (category.name !== config.name) {
                    ConsoleLogger.warn('WebhookPinger', `Name mismatch detected. Updating "${config.name}" -> "${category.name}"`);
                    logCallback(`[Config] Renamed "${config.name}" -> "${category.name}"`, true);
                    
                    config.name = category.name; // Update local config object
                    configuredCategories.set(id, config); // Update map
                    
                    // Update DB
                    try {
                        webhookConfig.addRawCategory(id, config.name); 
                    } catch (e) {
                        ConsoleLogger.error('WebhookPinger', 'Failed to update category name in DB:', e);
                    }
                }

                config.hooks = await prepareWebhooksForCategory(category, interaction.client, (msg) => logCallback(msg, false));
                
                // ZOMBIE CHECK 2: Double check after await
                if (!configuredCategories.has(id)) {
                    console.warn(`[${config.name}] Aborting lazy load: Category removed from config (Reset?).`);
                    continue;
                }

                configuredCategories.set(id, config); // Update cache
                
                ConsoleLogger.success(config.name, `Webhooks prepared.`);
                logCallback(`[${config.name}] Webhooks prepared.`, true);
            } catch (err) {
                ConsoleLogger.error(config.name, `Failed to load:`, err);
                logCallback(`[${config.name}] Failed to load: ${err.message}`, true);
                continue;
            }
        }
        
        startLoop(id, config, rounds, interaction, logCallback, startTime, executionState);
    }

    executionState.isInitializing = false;
    // Check if everything finished while we were still initializing (e.g. very fast rounds)
    if (activePingLoops.size === 0) {
        logSuccess(rounds, startTime, logCallback);
    }
}

// Helper for success log to avoid duplication
function logSuccess(rounds, startTime, logCallback) {
    const duration = ((Date.now() - startTime) / 1000).toFixed(2);
    let totalChannels = 0;
    for (const cfg of configuredCategories.values()) {
        if (cfg.hooks) totalChannels += cfg.hooks.length;
    }
    const catCount = configuredCategories.size;
    
    const rStr = rounds === 1 ? 'round' : 'rounds';
    const cStr = catCount === 1 ? 'category' : 'categories';
    const chStr = totalChannels === 1 ? 'channel' : 'channels';
    const totalPings = rounds * totalChannels;
    
    // Only log if we actually did something
    if (totalChannels > 0) {
        const msg = `Successfully initialized ${rounds} ${rStr} in ${catCount} ${cStr} and ${totalChannels} ${chStr} resulting in ${totalPings} pings in ${duration}s.`;
        ConsoleLogger.success('WebhookPinger', msg);
        if (logCallback) logCallback(`[WebhookPinger] ${msg}`, true);
    } else {
        const msg = `⚠️ Finished execution but no channels were targeted (configuration empty or load failed).`;
        ConsoleLogger.warn('WebhookPinger', msg);
        if (logCallback) logCallback(`[WebhookPinger] ${msg}`, true);
    }
}

// Internal loop starter
function startLoop(catId, config, rounds, interaction, logCallback, startTime, executionState) {
    let running = true;
    const stop = () => { running = false; };
    
    // Initial State
    const state = { stop, roundsTotal: rounds, currentRound: 0 };
    activePingLoops.set(catId, state);

    const log = (msg, force = false) => {
        ConsoleLogger.info(config.name, msg);
        if (logCallback) logCallback(msg, force);
    };

    (async () => {
        while (running && state.currentRound < rounds) {
            state.currentRound++;
            log(`Initializing round/s: ${state.currentRound}/${rounds}`, true);

            const BATCH_SIZE = 25;
            for (let i = 0; i < config.hooks.length; i += BATCH_SIZE) {
                if (!running) break;

                const batch = config.hooks.slice(i, i + BATCH_SIZE);
                await Promise.all(batch.map(async ({ hook, channelName }) => {
                    // Instant break check within async map (optimization)
                    if (!running) return;

                    try {
                        await hook.send({
                            content: '@everyone',
                            username: 'https://dc.gg/mindscape',
                            avatarURL: interaction.guild.iconURL(),
                        });
                    } catch (err) {
                        if (err.status === 429 || err.code === 429) {
                            let retryTime = 5000;
                            // Attempt to get retry_after from response body/headers if exposed by lib
                            if (err.rawError && err.rawError.retry_after) {
                                retryTime = (err.rawError.retry_after * 1000) + 500;
                            }
                            
                            ConsoleLogger.warn(`Run-${config.name}`, `⚠️ Rate Limit hit on ${channelName}. Backing off ${(retryTime/1000).toFixed(1)}s...`);
                            await sleep(retryTime); 
                        } else if (err.code === 10015) { // Unknown Webhook
                            ConsoleLogger.warn(`Run-${config.name}`, `⚠️ Webhook for ${channelName} is missing (404). Removing from list.`);
                            // Optional: Remove from memory to save future calls
                            // finding index is O(N) but safe here
                            const idx = config.hooks.findIndex(h => h.channelName === channelName);
                            if (idx > -1) config.hooks.splice(idx, 1);
                        } else {
                            ConsoleLogger.error(`Run-${config.name}`, `Failed to send to ${channelName}:`, err);
                        }
                    }
                }));
                
                // Post-batch check
                if (!running) break;

                await sleep(100); // Rate limit safety between batches
            }

            if (state.currentRound < rounds) {
                await sleep(LOOP_DELAY_MS);
            }
        }

        log(`Finished.`, true); 
        activePingLoops.delete(catId);
        
        // If all done, maybe notify?
        // Only log if we are NOT currently initializing (to avoid partial logs during lazy load)
        if (activePingLoops.size === 0 && !executionState?.isInitializing) {
            logSuccess(rounds, startTime, logCallback);
        }
    })();
}

// --- DB INTEGRATION ---
const { webhookConfig } = require('../utils/database');

/**
 * Initializes the script by loading configured categories from the DB.
 */
async function initialize(client) {
    const rows = webhookConfig.getAllRawCategories();
    ConsoleLogger.info('WebhookPinger', `Loading ${rows.length} configured categories from DB...`);

    for (const row of rows) {
        configuredCategories.set(row.categoryId, {
            name: row.categoryName,
            hooks: null // Lazy load
        });
    }
    ConsoleLogger.info('WebhookPinger', `Loaded configuration for ${rows.length} categories (Lazy).`);
}

module.exports = {
    initialize, // Export init
    listPingCategories,
    resetPingCategories,
    setPingCategory,
    runPingCategories
};
