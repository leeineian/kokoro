const { PermissionFlagsBits, ChannelType, MessageFlags } = require('discord.js');
const ConsoleLogger = require('../utils/log/consoleLogger');
const V2Builder = require('../utils/core/components');
const { webhookLooper: webhookRepo } = require('../utils/core/database');

// --- STATE ---
// Map<channelId, { config, hooks }>
const configuredChannels = new Map();
// Map<channelId, { stop: Function, roundsTotal: number, currentRound: number, intervalTimeout: NodeJS.Timeout }>
const activeLoops = new Map();

const NAME = 'LoopHook';
const LOOP_DELAY = 0; // Immediate execution between rounds
const BATCH_SIZE = 25;

// --- HELPERS ---
const sleep = (ms) => new Promise(resolve => setTimeout(resolve, ms));
const withTimeout = (promise, ms) => Promise.race([promise, new Promise((_, reject) => setTimeout(() => reject(new Error('Timeout')), ms))]);

/**
 * Parse interval string to milliseconds
 * @param {string|number} interval - e.g., "1min", "30s", "2h", or 0
 * @returns {number} Milliseconds, or 0 for infinite
 */
function parseInterval(interval) {
    if (typeof interval === 'number') return interval;
    if (interval === '0' || interval === 0) return 0;
    
    const match = interval.match(/^(\d+)(s|m|min|h|hr)?$/i);
    if (!match) throw new Error(`Invalid interval format: ${interval}`);
    
    const value = parseInt(match[1]);
    const unit = (match[2] || 's').toLowerCase();
    
    switch (unit) {
        case 's': return value * 1000;
        case 'm':
        case 'min': return value * 60 * 1000;
        case 'h':
        case 'hr': return value * 60 * 60 * 1000;
        default: throw new Error(`Unknown interval unit: ${unit}`);
    }
}

/**
 * Format milliseconds to human-readable interval
 */
function formatInterval(ms) {
    if (ms === 0) return 'infinite';
    if (ms < 60000) return `${ms / 1000}s`;
    if (ms < 3600000) return `${ms / 60000}min`;
    return `${ms / 3600000}h`;
}

/**
 * Rename channel or category
 */
async function renameChannel(channel, newName) {
    if (!newName) return;
    try {
        await channel.setName(newName);
        ConsoleLogger.info('WebhookLooper', `Renamed ${channel.type === ChannelType.GuildCategory ? 'category' : 'channel'} to: ${newName}`);
    } catch (err) {
        ConsoleLogger.error('WebhookLooper', `Failed to rename channel/category:`, err);
    }
}

/**
 * Lists configured and active loop channels with interactive deletion
 */
async function listLoopConfigs(interaction) {
    if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
        return interaction.reply({ content: '❌ **Access Denied.** Admin only.', flags: MessageFlags.Ephemeral });
    }

    if (configuredChannels.size === 0) {
        return interaction.reply({ content: 'ℹ️ No channels/categories are currently configured.', flags: MessageFlags.Ephemeral });
    }

    const configs = Array.from(configuredChannels.entries());
    const list = configs.map(([id, data]) => {
        const active = activeLoops.get(id);
        const typeIcon = data.config.channelType === 'category' ? '📁' : '💬';
        const roundsStr = data.config.rounds === 0 ? 'Random' : data.config.rounds;
        const intervalStr = formatInterval(data.config.interval);
        
        let status;
        if (active) {
            status = `🟢 **Running** (Round ${active.currentRound}/${active.roundsTotal})`;
        } else if (data.hooks === null) {
            status = `⚪ **Configured** (Lazy Loaded)`;
        } else {
            status = `🟠 **Configured** (Ready)`;
        }
        
        return `${typeIcon} **${data.config.channelName}** [${data.hooks ? data.hooks.length : '?'} channels] - Rounds: ${roundsStr}, Interval: ${intervalStr}\n    ${status}`;
    }).join('\n\n');

    // Build select menu for deletion
    const selectOptions = configs.map(([id, data]) => ({
        label: data.config.channelName,
        value: id,
        description: `${data.config.channelType} • Rounds: ${data.config.rounds === 0 ? 'Random' : data.config.rounds}`,
        emoji: data.config.channelType === 'category' ? '📁' : '💬'
    }));

    const components = [
        V2Builder.textDisplay(`**Loop Configurations:**\n${list}`),
        V2Builder.actionRow([
            V2Builder.selectMenu(
                'delete_loop_config',
                selectOptions,
                'Select a configuration to delete',
                1,
                1
            )
        ])
    ];

    const container = V2Builder.container(components);
    
    return interaction.reply({ 
        components: [container], 
        flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral 
    });
}

/**
 * Configure a channel or category for looping
 */
async function setLoopConfig(interaction) {
    if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
        return interaction.reply({ content: '❌ **Access Denied.** Admin only.', flags: MessageFlags.Ephemeral });
    }

    const channel = interaction.options.getChannel('channel');
    const intervalInput = interaction.options.getString('interval') ?? "0"; // Default: infinite random mode
    const activeChannelName = interaction.options.getString('active_name');
    const inactiveChannelName = interaction.options.getString('inactive_name');
    const loopMessage = interaction.options.getString('message');
    const showLogs = interaction.options.getBoolean('logs') ?? false;

    // Validate channel type
    const isCategory = channel.type === ChannelType.GuildCategory;
    const isTextChannel = channel.type === ChannelType.GuildText;
    
    if (!isCategory && !isTextChannel) {
        return interaction.reply({ 
            content: '❌ Please select either a **text channel** or a **category**.', 
            flags: MessageFlags.Ephemeral 
        });
    }
    
    // Check if configuration exists and notify overwrite (but proceed)
    if (configuredChannels.has(channel.id)) {
        // Just a log, we proceed to overwrite
        ConsoleLogger.info('WebhookLooper', `Overwriting existing configuration for channel: ${channel.name}`);
    }

    // Parse interval
    let interval;
    try {
        interval = parseInterval(intervalInput);
    } catch (err) {
        return interaction.reply({ content: `❌ ${err.message}`, flags: MessageFlags.Ephemeral });
    }

    // --- PERMISSION VALIDATION ---
    const botMember = interaction.guild.members.me;
    
    if (isCategory) {
        // Check bot has Manage Webhooks permission in category
        if (!channel.permissionsFor(botMember).has(PermissionFlagsBits.ManageWebhooks)) {
            return interaction.reply({ 
                content: `❌ **Permission Error**\n\nI need the \`Manage Webhooks\` permission in the category **${channel.name}**.\n\nPlease grant this permission and try again.`, 
                flags: MessageFlags.Ephemeral 
            });
        }

        // Check permissions in child text channels
        const textChannels = channel.children.cache.filter(c => c.type === ChannelType.GuildText);
        
        if (textChannels.size === 0) {
            return interaction.reply({ 
                content: `❌ **No Text Channels**\n\nThe category **${channel.name}** has no text channels to set up webhooks in.`, 
                flags: MessageFlags.Ephemeral 
            });
        }

        const missingPerms = [];
        for (const [id, ch] of textChannels) {
            const perms = ch.permissionsFor(botMember);
            if (!perms.has(PermissionFlagsBits.ViewChannel) || 
                !perms.has(PermissionFlagsBits.SendMessages)) {
                missingPerms.push(ch.name);
            }
        }

        if (missingPerms.length > 0) {
            const list = missingPerms.slice(0, 10).map(n => `• ${n}`).join('\n');
            const more = missingPerms.length > 10 ? `\n... and ${missingPerms.length - 10} more` : '';
            
            return interaction.reply({
                content: `❌ **Permission Error**\n\nI cannot access the following channels in **${channel.name}**:\n${list}${more}\n\nPlease grant me \`View Channel\` and \`Send Messages\` permissions.`,
                flags: MessageFlags.Ephemeral
            });
        }
    } else {
        // Validate text channel permissions
        const perms = channel.permissionsFor(botMember);
        if (!perms.has(PermissionFlagsBits.ManageWebhooks) ||
            !perms.has(PermissionFlagsBits.ViewChannel) ||
            !perms.has(PermissionFlagsBits.SendMessages)) {
            return interaction.reply({
                content: `❌ **Permission Error**\n\nI need the following permissions in **${channel.name}**:\n• Manage Webhooks\n• View Channel\n• Send Messages`,
                flags: MessageFlags.Ephemeral
            });
        }
    }
    // --- END PERMISSION VALIDATION ---

    await interaction.deferReply();
    await interaction.editReply({ content: '⏳ Initializing...' });
    const message = await interaction.fetchReply();

    // Prepare config
    const config = {
        channelId: channel.id,
        channelName: channel.name,
        channelType: isCategory ? 'category' : 'channel',
        rounds: 0,
        interval,
        activeChannelName,
        inactiveChannelName,
        message: loopMessage
    };

    // Save to DB
    try {
        webhookRepo.addLoopConfig(channel.id, config);
    } catch (err) {
        ConsoleLogger.error('WebhookLooper', 'DB Error:', err);
        return message.edit('❌ Failed to save configuration to database.');
    }

    // Log Buffer for UI
    const logs = [];
    const updateInterval = 1500;
    let lastUpdate = 0;
    let updateInProgress = false;

    const updateUI = async (final = false) => {
        const now = Date.now();
        // Skip if update already in progress or too soon
        if (updateInProgress || (!final && now - lastUpdate < updateInterval)) return;
        
        updateInProgress = true;
        lastUpdate = now;

        const components = [
            V2Builder.textDisplay(`Processing **${channel.name}**...`)
        ];

        if (showLogs) {
            const logContent = logs.slice(-15).join('\n');
            components.push(V2Builder.textDisplay(`\`\`\`log\n${logContent}\n\`\`\``));
        }

        const container = V2Builder.container(components);

        await message.edit({ 
            content: null,
            components: [container],
            flags: MessageFlags.IsComponentsV2
        }).catch(err => ConsoleLogger.error('WebhookLooper', 'UI Update Error:', err))
          .finally(() => { updateInProgress = false; });
    };

    const stripAnsi = (str) => str.replace(/\x1B\[\d+m/g, '');

    const logCallback = (msg) => {
        logs.push(stripAnsi(msg));
        // Don't await - fire and forget to prevent blocking
        updateUI().catch(() => {});
    };

    let hooks;
    try {
        if (isCategory) {
            hooks = await prepareWebhooksForCategory(channel, interaction.client, logCallback);
        } else {
            hooks = await prepareWebhooksForChannel(channel, interaction.client, logCallback);
        }
        await updateUI(true);
    } catch (error) {
        ConsoleLogger.error(`Setup-${channel.name}`, `Error:`, error);
        return message.edit(`❌ Error setting up webhooks: ${error.message}`);
    }

    // Save Configuration
    configuredChannels.set(channel.id, { config, hooks });

    const successContainer = V2Builder.container([
        V2Builder.textDisplay(
            `✅ **${isCategory ? 'Category' : 'Channel'} Configured**\n` +
            `> **${channel.name}**\n` +
            `> Prepared ${hooks.length} webhook(s).\n` +
            `> Interval: ${formatInterval(interval)}\n` +
            `> Run \`/debug webhook-looper start\` to begin.`
        )
    ]);

    return message.edit({ 
        content: null,
        components: [successContainer],
        flags: MessageFlags.IsComponentsV2
    });
}

/**
 * Helper to fetch/create webhooks for a single channel
 */
async function prepareWebhooksForChannel(channel, client, logCallback) {
    try {
        const msg = `[Setup-${channel.name}] Processing channel: ${channel.name}`;
        ConsoleLogger.info('Setup', msg);
        if (logCallback) logCallback(msg);

        const existingHooks = await channel.fetchWebhooks();
        let hook = existingHooks.find(h => h.owner.id === client.user.id && h.name === NAME);

        if (!hook) {
            if (existingHooks.size >= 10) {
                const warning = `[Setup-${channel.name}] ⚠️ Channel '${channel.name}' has 10 webhooks and none belong to us. Skipping.`;
                ConsoleLogger.warn('Setup', warning);
                if (logCallback) logCallback(warning);
                return [];
            }
            hook = await channel.createWebhook({
                name: NAME,
                avatar: client.user.displayAvatarURL(),
            });
        }
        
        return [{ hook, channelName: channel.name }];
    } catch (err) {
        ConsoleLogger.error('Setup', `Failed to setup channel '${channel.name}':`, err);
        if (logCallback) logCallback(`[Setup] ❌ Failed: ${err.message}`);
        return [];
    }
}

/**
 * Helper to fetch/create webhooks for a category
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
                if (attempts === 1) {
                    i++;
                    const msg = `[Setup-${category.name}] Processing channel ${i}/${targetChannels.size}: ${channel.name}`;
                    ConsoleLogger.info('Setup', msg);
                    if (logCallback) logCallback(msg);
                }

                const existingHooks = await withTimeout(channel.fetchWebhooks(), 20000);
                let hook = existingHooks.find(h => h.owner.id === client.user.id && h.name === NAME);

                if (!hook) {
                    if (existingHooks.size >= 10) {
                        const warning = `[Setup-${category.name}] ⚠️ Channel '${channel.name}' has 10 webhooks and none belong to us. Skipping.`;
                        ConsoleLogger.warn('Setup', warning);
                        if (logCallback) logCallback(warning);
                        success = true;
                        continue;
                    } else {
                        hook = await withTimeout(channel.createWebhook({
                            name: NAME,
                            avatar: client.user.displayAvatarURL(), 
                        }), 20000);
                    }
                }
                if (hook) hooks.push({ hook, channelName: channel.name });
                success = true;
            } catch (err) {
                if (err.status === 429 || err.code === 429) {
                    let retryTime = 5000;
                    if (err.retry_after) retryTime = err.retry_after * 1000;
                    if (err.global) retryTime += 100;
                    
                    ConsoleLogger.warn('Setup', `[Setup-${category.name}] ⚠️ Rate Limit on '${channel.name}'. Waiting ${(retryTime/1000).toFixed(1)}s (Attempt ${attempts}/3)...`);
                    if (logCallback) logCallback(`[Setup-${category.name}] ⚠️ Rate Limit. Retrying in ${(retryTime/1000).toFixed(1)}s...`);
                    
                    await sleep(retryTime);
                    continue;
                }

                if (err.message === 'Timeout') {
                    ConsoleLogger.warn('Setup', `[Setup-${category.name}] ⚠️ Timeout on '${channel.name}'. Retrying (Attempt ${attempts}/3)...`);
                    if (logCallback) logCallback(`[Setup-${category.name}] ⚠️ Timeout. Retrying...`);
                    await sleep(2000);
                    continue;
                }

                const errName = err.code === 50013 ? 'Missing Permissions' : err.message;
                const errorMsg = `[Setup-${category.name}] ❌ Failed to setup channel '${channel.name}': ${errName}. Skipping.`;
                ConsoleLogger.error('Setup', errorMsg);
                if (logCallback) logCallback(errorMsg);
                success = true;
            }
            
            // Small delay between channels to avoid rate limits
            if (success) {
                await sleep(10);
            }
        }
    }
    return hooks;
}

/**
 * Start loops for all configured channels
 */
async function startLoops(interaction) {
    if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
        return interaction.reply({ content: '❌ **Access Denied.** Admin only.', flags: MessageFlags.Ephemeral });
    }

    if (configuredChannels.size === 0) {
        return interaction.reply({ content: '❌ No channels configured! Use \`/debug webhook-looper set\` first.', flags: MessageFlags.Ephemeral });
    }

    if (activeLoops.size > 0) {
        return interaction.reply({ content: '⚠️ Loops are already running! Wait for them to finish or stop them first.', flags: MessageFlags.Ephemeral });
    }

    await interaction.deferReply();
    await interaction.editReply({ content: '⏳ Starting webhook looper...' });
    const message = await interaction.fetchReply();
    const showLogs = interaction.options.getBoolean('logs') ?? false;

    // Log Buffer for UI
    const logs = [];
    const updateInterval = 1500;
    let lastUpdate = 0;

    const updateUI = async (force = false) => {
        const now = Date.now();
        if (!force && now - lastUpdate < updateInterval) return;
        lastUpdate = now;

        const components = [];
        
        if (showLogs) {
             const logContent = logs.join('\n');
             components.push(V2Builder.textDisplay(`\`\`\`log\n${logContent}\n\`\`\``));
        } else {
             components.push(V2Builder.textDisplay(`🚀 **Running...**\nChannels: ${totalChannels} | Configs: ${configCount}`));
        }

        const container = V2Builder.container(components);

        await message.edit({ 
            content: null,
            components: [container],
            flags: MessageFlags.IsComponentsV2
        }).catch(err => ConsoleLogger.error('WebhookLooper', 'UI Update Error:', err));
    };

    const stripAnsi = (str) => str.replace(/\x1B\[\d+m/g, '');

    const logCallback = (msg, force = false) => {
        if (logs.length >= 50) logs.shift();
        logs.push(stripAnsi(msg));
        updateUI(force);
    };

    let totalChannels = 0;
    for (const { hooks } of configuredChannels.values()) {
        if (hooks) totalChannels += hooks.length;
    }
    const configCount = configuredChannels.size;

    const startMsg = `🚀 Configs: ${configCount} | Channels: ${totalChannels}`;
    ConsoleLogger.success('WebhookLooper', startMsg);
    logs.push(startMsg);
    await updateUI(true);

    const startTime = Date.now();
    const channelsToRun = Array.from(configuredChannels.entries());

    const executionState = { isInitializing: true };

    for (const [id, data] of channelsToRun) {
        if (!configuredChannels.has(id)) continue;

        // Lazy Load check
        if (!data.hooks) {
            ConsoleLogger.info(data.config.channelName, `Lazy loading webhooks...`);
            logCallback(`[${data.config.channelName}] Lazy loading webhooks...`, true);
            try {
                const channel = await interaction.client.channels.fetch(id).catch(() => null);
                if (!channel) {
                    ConsoleLogger.warn(data.config.channelName, `Channel not found! Automatically removing from config.`);
                    logCallback(`[${data.config.channelName}] Channel not found! Auto-removing...`, true);
                    
                    configuredChannels.delete(id);
                    configuredChannels.delete(id);
                    webhookRepo.deleteLoopConfig(id);
                    continue;
                }

                // Sync Name if changed
                if (channel.name !== data.config.channelName) {
                    ConsoleLogger.warn('WebhookLooper', `Name mismatch detected. Updating "${data.config.channelName}" -> "${channel.name}"`);
                    logCallback(`[Config] Renamed "${data.config.channelName}" -> "${channel.name}"`, true);
                    
                    data.config.channelName = channel.name;
                    configuredChannels.set(id, data);
                    
                    try {
                        webhookRepo.addLoopConfig(id, data.config);
                    } catch (e) {
                        ConsoleLogger.error('WebhookLooper', 'Failed to update channel name in DB:', e);
                    }
                }

                if (data.config.channelType === 'category') {
                    data.hooks = await prepareWebhooksForCategory(channel, interaction.client, (msg) => logCallback(msg, false));
                } else {
                    data.hooks = await prepareWebhooksForChannel(channel, interaction.client, (msg) => logCallback(msg, false));
                }
                
                if (!configuredChannels.has(id)) {
                    ConsoleLogger.warn(data.config.channelName, `Aborting lazy load: Channel removed from config.`);
                    continue;
                }

                configuredChannels.set(id, data);
                
                ConsoleLogger.success(data.config.channelName, `Webhooks prepared.`);
                logCallback(`[${data.config.channelName}] Webhooks prepared.`, true);
            } catch (err) {
                ConsoleLogger.error(data.config.channelName, `Failed to load:`, err);
                logCallback(`[${data.config.channelName}] Failed to load: ${err.message}`, true);
                continue;
            }
        }
        
        startLoop(id, data, interaction, logCallback, startTime, executionState);
    }

    executionState.isInitializing = false;
    if (activeLoops.size === 0) {
        logSuccess(startTime, logCallback);
    }
}

// Helper for success log
function logSuccess(startTime, logCallback) {
    const duration = ((Date.now() - startTime) / 1000).toFixed(2);
    let totalChannels = 0;
    let totalRounds = 0;
    
    for (const { hooks, config } of configuredChannels.values()) {
        if (hooks) {
            totalChannels += hooks.length;
            totalRounds += (config.rounds === 0 ? 5 : config.rounds) * hooks.length; // Estimate for random
        }
    }
    
    const configCount = configuredChannels.size;
    
    if (totalChannels > 0) {
        const msg = `Successfully initialized ${configCount} config(s) with ${totalChannels} channel(s) in ${duration}s.`;
        ConsoleLogger.success('WebhookLooper', msg);
        if (logCallback) logCallback(`[WebhookLooper] ${msg}`, true);
    } else {
        const msg = `⚠️ Finished execution but no channels were targeted.`;
        ConsoleLogger.warn('WebhookLooper', msg);
        if (logCallback) logCallback(`[WebhookLooper] ${msg}`, true);
    }
}

// Internal loop starter
function startLoop(channelId, data, interaction, logCallback, startTime, executionState) {
    let running = true;
    const stop = () => { running = false; };
    
    const state = { stop, roundsTotal: 0, currentRound: 0, intervalTimeout: null };
    activeLoops.set(channelId, state);
    webhookRepo.setLoopState(channelId, true); // Persist state

    const log = (msg, force = false) => {
        ConsoleLogger.info(data.config.channelName, msg);
        if (logCallback) logCallback(msg, force);
    };

    // Rename channel to active name
    (async () => {
        const channel = await interaction.client.channels.fetch(channelId).catch(() => null);
        if (channel && data.config.activeChannelName) {
            await renameChannel(channel, data.config.activeChannelName);
        }
    })();

    let gracefulStop = false; // Flag for finishing last round after timeout

    // NEW LOGIC:
    // If interval > 0: Loop continuously until interval time expires (ignore rounds)
    // If interval = 0: Infinite random mode (1-100 rounds, 1-10min delays)
    
    const isTimedMode = data.config.interval > 0;
    const isRandomInfiniteMode = data.config.interval === 0;
    
    if (isTimedMode) {
        // Timed mode: loop continuously until interval expires
        log(`[${data.config.channelName}] Starting timed loop for ${formatInterval(data.config.interval)}`, true);
        
        state.intervalTimeout = setTimeout(async () => {
            log(`[${data.config.channelName}] ⏰ Time limit reached. Finishing last round...`, true);
            gracefulStop = true;
            // We do NOT call stop() here. We let the loop finish its current iteration.
        }, data.config.interval);
    } else if (isRandomInfiniteMode) {
        // Random infinite mode
        log(`[${data.config.channelName}] Starting infinite random mode`, true);
    }

    (async () => {
        while (running) {
            if (isRandomInfiniteMode) {
                // Generate random rounds and delay for this iteration
                const randomRounds = Math.floor(Math.random() * 100) + 1; // 1-100
                const randomDelay = (Math.floor(Math.random() * 10) + 1) * 60 * 1000; // 1-10 minutes in ms
                
                state.roundsTotal = randomRounds;
                state.currentRound = 0;
                
                log(`[${data.config.channelName}] 🎲 Random: ${randomRounds} rounds, next delay: ${formatInterval(randomDelay)}`, true);
                
                // Execute the random rounds
                for (let i = 0; i < randomRounds && running; i++) {
                    state.currentRound = i + 1;
                    log(`[${data.config.channelName}] Round ${state.currentRound}/${randomRounds}`, true);
                    
                    await executeRound(data, running, log, interaction);
                    
                    if (!running) break;
                    if (i < randomRounds - 1) {
                        await sleep(LOOP_DELAY);
                    }
                }
                
                // Wait random delay before next iteration
                if (running) {
                    log(`[${data.config.channelName}] ⏳ Waiting ${formatInterval(randomDelay)} before next iteration...`, true);
                    await sleep(randomDelay);
                }
            } else if (isTimedMode) {
                // Timed mode: run until gracefulStop is set (or manual stop)
                // The loop condition while(running) catches manual stops.
                // We check gracefulStop inside.
                
                if (gracefulStop) break;

                state.currentRound++;
                log(`[${data.config.channelName}] Round ${state.currentRound}`, true);
                
                await executeRound(data, running, log, interaction);
                
                if (running && !gracefulStop) {
                    await sleep(LOOP_DELAY);
                }
            } else {
                // Fallback: use rounds parameter (old behavior for compatibility)
                const roundsTotal = data.config.rounds === 0 ? Math.floor(Math.random() * 10) + 1 : data.config.rounds;
                state.roundsTotal = roundsTotal;
                
                while (running && state.currentRound < roundsTotal) {
                    state.currentRound++;
                    log(`[${data.config.channelName}] Round ${state.currentRound}/${roundsTotal}`, true);
                    
                    await executeRound(data, running, log, interaction);
                    
                    if (state.currentRound < roundsTotal && running) {
                        await sleep(LOOP_DELAY);
                    }
                }
                break; // Exit main while loop after rounds complete
            }
        }

        log(`[${data.config.channelName}] Finished.`, true);
        
        // Clear interval timeout if set
        if (state.intervalTimeout) {
            clearTimeout(state.intervalTimeout);
        }
        
        // Rename to inactive
        const channel = await interaction.client.channels.fetch(channelId).catch(() => null);
        if (channel && data.config.inactiveChannelName) {
            await renameChannel(channel, data.config.inactiveChannelName);
        }
        
        activeLoops.delete(channelId);
        webhookRepo.setLoopState(channelId, false); // Persist state
        
        if (activeLoops.size === 0 && !executionState?.isInitializing) {
            logSuccess(startTime, logCallback);
        }
    })();
}

// Helper to execute a single round of webhook sends
async function executeRound(data, running, log, interaction) {
    for (let i = 0; i < data.hooks.length; i += BATCH_SIZE) {
        if (!running) break;

        const batch = data.hooks.slice(i, i + BATCH_SIZE);
        await Promise.all(batch.map(async ({ hook, channelName }) => {
            if (!running) return;

            try {
                await hook.send({
                    content: data.config.message || '@everyone',
                    username: 'https://dc.gg/mindscape',
                    avatarURL: interaction.client.user.displayAvatarURL(),
                });
            } catch (err) {
                if (err.status === 429 || err.code === 429) {
                    let retryTime = 5000;
                    if (err.rawError && err.rawError.retry_after) {
                        retryTime = (err.rawError.retry_after * 1000) + 50;
                    }
                    
                    ConsoleLogger.warn(`Run-${data.config.channelName}`, `⚠️ Rate Limit hit on ${channelName}. Backing off ${(retryTime/1000).toFixed(1)}s...`);
                    await sleep(retryTime); 
                } else if (err.code === 10015) {
                    ConsoleLogger.warn(`Run-${data.config.channelName}`, `⚠️ Webhook for ${channelName} is missing (404). Removing from list.`);
                    const idx = data.hooks.findIndex(h => h.channelName === channelName);
                    if (idx > -1) data.hooks.splice(idx, 1);
                } else {
                    ConsoleLogger.error(`Run-${data.config.channelName}`, `Failed to send to ${channelName}:`, err);
                }
            }
        }));
        
        if (!running) break;
        await sleep(0);
    }
}

/**
 * Stop running loops (optionally with selection)
 */
async function stopLoops(interaction) {
    if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
        return interaction.reply({ content: '❌ **Access Denied.** Admin only.', flags: MessageFlags.Ephemeral });
    }

    if (activeLoops.size === 0) {
        return interaction.reply({ content: 'ℹ️ No loops are currently running.', flags: MessageFlags.Ephemeral });
    }

    // Build selection UI
    const activeConfigs = Array.from(activeLoops.entries()).map(([id, state]) => {
        const data = configuredChannels.get(id);
        return { id, state, config: data?.config };
    }).filter(item => item.config);

    if (activeConfigs.length === 1) {
        // Only one running, stop it directly
        const { id, state, config } = activeConfigs[0];
        state.stop();
        if (state.intervalTimeout) clearTimeout(state.intervalTimeout);
        
        // Rename to inactive
        const channel = await interaction.client.channels.fetch(id).catch(() => null);
        if (channel && config.inactiveChannelName) {
            await renameChannel(channel, config.inactiveChannelName);
        }
        
        activeLoops.delete(id);
        webhookRepo.setLoopState(id, false);
        
        ConsoleLogger.warn('WebhookLooper', `Stopped loop for: ${config.channelName}`);
        return interaction.reply({ content: `✅ Stopped loop for **${config.channelName}**.`, flags: MessageFlags.Ephemeral });
    }

    // Multiple loops running - show selection
    const selectOptions = activeConfigs.map(({ id, config, state }) => ({
        label: config.channelName,
        value: id,
        description: `Round ${state.currentRound}/${state.roundsTotal}`,
        emoji: config.channelType === 'category' ? '📁' : '💬'
    }));

    // Add "Stop All" option
    selectOptions.unshift({
        label: '🛑 Stop All',
        value: '__STOP_ALL__',
        description: `Stop all ${activeLoops.size} running loops`
    });

    const list = activeConfigs.map(({ config, state }) => 
        `• **${config.channelName}** - Round ${state.currentRound}/${state.roundsTotal}`
    ).join('\n');

    const components = [
        V2Builder.textDisplay(`**Active Loops:**\n${list}`),
        V2Builder.actionRow([
            V2Builder.selectMenu(
                'stop_loop_select',
                selectOptions,
                'Select loop(s) to stop',
                1,
                1
            )
        ])
    ];

    const container = V2Builder.container(components);
    
    return interaction.reply({ 
        content: null,
        components: [container], 
        flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral 
    });
}

async function initialize(client) {
    const rows = webhookRepo.getAllLoopConfigs();
    ConsoleLogger.info('WebhookLooper', `Loading ${rows.length} configured channels from DB...`);

    for (const row of rows) {
        configuredChannels.set(row.channelId, {
            config: {
                channelId: row.channelId,
                channelName: row.channelName,
                channelType: row.channelType,
                rounds: row.rounds,
                interval: row.interval,
                activeChannelName: row.activeChannelName,
                inactiveChannelName: row.inactiveChannelName,
                message: row.message,
                isRunning: row.isRunning === 1
            },
            hooks: null // Lazy load
        });
    }
    ConsoleLogger.info('WebhookLooper', `Loaded configuration for ${rows.length} channels (Lazy).`);
    
    // Auto-resume active loops
    const resumeTargets = Array.from(configuredChannels.entries()).filter(([_, data]) => data.config.isRunning);
    
    if (resumeTargets.length > 0) {
        ConsoleLogger.info('WebhookLooper', `Resuming ${resumeTargets.length} active loops...`);
        const startTime = Date.now();
        
        for (const [id, data] of resumeTargets) {
            // Create mock interaction for internal use
            const mockInteraction = {
                client: client,
                guild: client.guilds.cache.first(), // Fallback, not ideal but sufficient for fetching channels
                
                // Helper to perform channel operations without real interaction
                options: {
                     getChannel: () => null // Not used in startLoop
                }
            };
            
            // We need to load hooks first for these
            if (!data.hooks) {
                 try {
                    const channel = await client.channels.fetch(id).catch(() => null);
                    if (!channel) continue;
                    
                    if (data.config.channelType === 'category') {
                        data.hooks = await prepareWebhooksForCategory(channel, client, null);
                    } else {
                        data.hooks = await prepareWebhooksForChannel(channel, client, null);
                    }
                    configuredChannels.set(id, data);
                 } catch (e) {
                     ConsoleLogger.error('WebhookLooper', `Failed to resume ${data.config.channelName}:`, e);
                     continue;
                 }
            }
            
            startLoop(id, data, mockInteraction, null, startTime, { isInitializing: true });
        }
    }
    
    // Ensure all channels are in inactive state on startup
    // Ensure all channels are in inactive state on startup (Batched for performance)
    const updates = [];
    for (const [channelId, data] of configuredChannels) {
        if (data.config.inactiveChannelName) {
            updates.push({ channelId, name: data.config.inactiveChannelName });
        }
    }

    if (updates.length > 0) {
        ConsoleLogger.info('WebhookLooper', `Ensuring ${updates.length} channels are in inactive state...`);
        // Process in batches of 5 to avoid potential rate limits
        for (let i = 0; i < updates.length; i += 5) {
            const batch = updates.slice(i, i + 5);
            await Promise.all(batch.map(async ({ channelId, name }) => {
                const channel = await client.channels.fetch(channelId).catch(() => null);
                if (channel) {
                    await renameChannel(channel, name);
                }
            }));
            if (i + 5 < updates.length) await sleep(500); // Small delay between batches
        }
    }
}

module.exports = {
    initialize,
    listLoopConfigs,
    setLoopConfig,
    startLoops,
    stopLoops
};
