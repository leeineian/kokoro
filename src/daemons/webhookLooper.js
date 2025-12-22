const { PermissionFlagsBits, ChannelType, MessageFlags } = require('discord.js');
const ConsoleLogger = require('../utils/log/consoleLogger');
const V2Builder = require('../utils/core/components');
const { webhookLooper: webhookRepo } = require('../utils/core/database');
const { ANSI, title, key, val, formatInterval } = require('../commands/debug/.helper');
const statusRotator = require('./statusRotator');

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
 * Rename channel or category
 * @param {import('discord.js').GuildChannel} channel 
 * @param {string} newName 
 */
async function renameChannel(channel, newName) {
    if (!newName || channel.name === newName) return;
    try {
        await channel.setName(newName);
        ConsoleLogger.info('WebhookLooper', `Renamed ${channel.type === ChannelType.GuildCategory ? 'category' : 'channel'} to: ${newName}`);
    } catch (err) {
        ConsoleLogger.error('WebhookLooper', `Failed to rename channel/category:`, err);
    }
}

/**
 * Check if a thread was created by the bot
 * @param {import('discord.js').ThreadChannel} thread 
 * @param {import('discord.js').Client} client 
 * @returns {boolean}
 */
function isThreadCreatedByBot(thread, client) {
    return thread.ownerId === client.user.id;
}

/**
 * Find an existing bot-created thread or create a new one
 * @param {import('discord.js').TextChannel} channel 
 * @param {import('discord.js').Client} client 
 * @returns {Promise<import('discord.js').ThreadChannel|null>}
 */
async function findOrCreateThread(channel, client) {
    try {
        // Fetch active threads
        const activeThreads = await channel.threads.fetchActive();
        
        // Look for an existing thread with matching name created by bot
        const existingThread = activeThreads.threads.find(thread => 
            thread.name === channel.name && isThreadCreatedByBot(thread, client)
        );
        
        if (existingThread) {
            return existingThread;
        }
        
        // Check archived threads as well
        const archivedThreads = await channel.threads.fetchArchived();
        const archivedThread = archivedThreads.threads.find(thread =>
            thread.name === channel.name && isThreadCreatedByBot(thread, client)
        );
        
        if (archivedThread) {
            // Unarchive it if possible
            if (archivedThread.archived) {
                await archivedThread.setArchived(false).catch(() => {});
            }
            return archivedThread;
        }
        
        // Create new thread
        const thread = await channel.threads.create({
            name: channel.name,
            autoArchiveDuration: 10080, // 7 days
            reason: 'Webhook looper thread'
        });
        
        return thread;
    } catch (err) {
        ConsoleLogger.error('WebhookLooper', `Failed to find/create thread for ${channel.name}:`, err);
        return null;
    }
}

/**
 * Load threads for a configuration from the database
 * @param {string} id - Channel/Category ID
 * @param {Object} data - Configuration data object
 * @param {import('discord.js').Client} client - Discord client
 * @returns {Promise<Array>} Array of thread objects with hook, threadId, threadName, and channelId
 */
async function loadThreadsForConfig(id, data, client) {
    if (!data.config.useThread) {
        return [];
    }
    
    if (data.threads) {
        return data.threads; // Already loaded
    }
    
    try {
        // Get thread mappings from database
        const dbConfig = webhookRepo.getLoopConfig(id);
        const threadMappings = dbConfig?.threads ? JSON.parse(dbConfig.threads) : {};
        
        // Prepare thread data with webhooks
        const threads = [];
        for (const [parentChannelId, threadId] of Object.entries(threadMappings)) {
            // Fetch the parent channel to get its name
            const parentChannel = await client.channels.fetch(parentChannelId).catch(() => null);
            if (!parentChannel) continue;
            
            // Find the webhook for this parent channel by matching channel name
            const hookData = data.hooks.find(h => h.channelName === parentChannel.name);
            if (hookData) {
                // Fetch the thread to get its name
                const thread = await client.channels.fetch(threadId).catch(() => null);
                if (thread) {
                    threads.push({
                        hook: hookData.hook,
                        threadId: threadId,
                        threadName: thread.name,
                        channelId: parentChannelId
                    });
                }
            }
        }
        
        return threads;
    } catch (err) {
        ConsoleLogger.error(data.config.channelName || 'WebhookLooper', `Failed to load threads:`, err);
        return [];
    }
}

/**
 * Sync base names from Discord (updates DB if changed while NOT running)
 */
async function syncBaseNames(client) {
    let synced = 0;
    for (const [id, data] of configuredChannels) {
        if (activeLoops.has(id)) continue; // Don't sync while running

        const channel = await client.channels.fetch(id).catch(() => null);
        if (channel && channel.name !== data.config.channelName) {
            // Only update if it's not the active name (which is temporary)
            // We allow syncing TO the inactive name because that's often the intended base state
            if (channel.name !== data.config.activeChannelName) {
                data.config.channelName = channel.name;
                webhookRepo.updateChannelName(id, channel.name);
                synced++;
            }
        }
    }
    if (synced > 0) ConsoleLogger.info('WebhookLooper', `Synced ${synced} base names from Discord.`);
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

    // Defer because we might fetch many channels
    await interaction.deferReply({ flags: MessageFlags.Ephemeral });

    const configs = Array.from(configuredChannels.entries());
    const listItems = await Promise.all(configs.map(async ([id, data]) => {
        const active = activeLoops.get(id);
        const channel = await interaction.client.channels.fetch(id).catch(() => null);
        
        // Use live name if available, otherwise fallback to stored configuration name
        const displayName = channel?.name || data.config.channelName;
        
        const typeIcon = data.config.channelType === 'category' ? '📁' : '💬';
        const intervalStr = formatInterval(data.config.interval);
        
        let status;
        if (active) {
            status = `🟢 **Running** (Round ${active.currentRound}/${active.roundsTotal})`;
        } else if (data.hooks === null) {
            status = `⚪ **Configured** (Lazy Loaded)`;
        } else {
            status = `🟠 **Configured** (Ready)`;
        }
        
        return `${typeIcon} **${displayName}** [${data.hooks ? data.hooks.length : '?'} channels] - Interval: ${intervalStr}\n    ${status}`;
    }));

    const list = listItems.join('\n\n');

    // Build select menu for deletion (always use live names if possible)
    const selectOptions = await Promise.all(configs.map(async ([id, data]) => {
        const channel = await interaction.client.channels.fetch(id).catch(() => null);
        const displayName = channel?.name || data.config.channelName;
        const intervalStr = formatInterval(data.config.interval);

        return {
            label: displayName.substring(0, 100),
            value: id,
            description: `${data.config.channelType} • Interval: ${intervalStr}`,
            emoji: data.config.channelType === 'category' ? '📁' : '💬'
        };
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
    
    return interaction.editReply({ 
        content: null,
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
    const activeChannelName = interaction.options.getString('active_name');
    const inactiveChannelName = interaction.options.getString('inactive_name');
    const loopMessage = interaction.options.getString('message');
    const webhookAvatar = interaction.options.getString('webhook_avatar') ?? interaction.guild.iconURL();
    const webhookAuthor = interaction.options.getString('webhook_author') ?? 'LoopHook';
    const showLogs = interaction.options.getBoolean('logs') ?? false;
    const useThread = interaction.options.getBoolean('usethread') ?? false;
    const threadMessage = interaction.options.getString('threadmessage') || loopMessage;

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
    const interval = 0; // Always default to infinite random mode during config

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
        message: loopMessage,
        webhookAuthor,
        webhookAvatar,
        useThread,
        threadMessage
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
            components.push(V2Builder.textDisplay(`\`\`\`ansi\n${logContent}\n\`\`\``));
        }

        const container = V2Builder.container(components);

        await message.edit({ 
            content: null,
            components: [container],
            flags: MessageFlags.IsComponentsV2
        }).catch(err => ConsoleLogger.error('WebhookLooper', 'UI Update Error:', err))
          .finally(() => { updateInProgress = false; });
    };

    const logCallback = (msg) => {
        logs.push(msg);
        // Don't await - fire and forget to prevent blocking
        updateUI().catch(() => {});
    };

    let hooks;
    let threadData = [];
    try {
        if (isCategory) {
            hooks = await prepareWebhooksForCategory(channel, interaction.client, logCallback);
            if (useThread) {
                threadData = await prepareThreadsForCategory(channel, interaction.client, logCallback);
            }
        } else {
            hooks = await prepareWebhooksForChannel(channel, interaction.client, logCallback);
            if (useThread) {
                threadData = await prepareThreadsForChannel(channel, interaction.client, logCallback);
            }
        }
        await updateUI(true);
    } catch (error) {
        ConsoleLogger.error(`${channel.name}`, `Error:`, error);
        return message.edit(`❌ Error setting up webhooks: ${error.message}`);
    }

    // Save thread mappings if threads were created
    if (useThread && threadData.length > 0) {
        const threadMappings = {};
        for (const { threadId, channelId } of threadData) {
            threadMappings[channelId] = threadId;
        }
        try {
            webhookRepo.setThreadMappings(channel.id, threadMappings);
        } catch (err) {
            ConsoleLogger.error('WebhookLooper', 'Failed to save thread mappings:', err);
        }
    }

    // Save Configuration
    configuredChannels.set(channel.id, { config, hooks });

    const threadInfo = useThread ? `\n> Prepared ${threadData.length} thread(s).` : '';
    const successContainer = V2Builder.container([
        V2Builder.textDisplay(
            `✅ **${isCategory ? 'Category' : 'Channel'} Configured**\n` +
            `> **${channel.name}**\n` +
            `> Prepared ${hooks.length} webhook(s).${threadInfo}\n` +
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
        const msg = `[${channel.name}] Processing channel: ${channel.name}`;
        if (logCallback) logCallback(msg);

        const existingHooks = await channel.fetchWebhooks();
        let hook = existingHooks.find(h => h.owner.id === client.user.id && h.name === NAME);

        if (!hook) {
            if (existingHooks.size >= 10) {
                const warning = `[${channel.name}] ⚠️ Channel '${channel.name}' has 10 webhooks and none belong to us. Skipping.`;
                ConsoleLogger.warn('Setup', warning);
                if (logCallback) logCallback(warning);
                return [];
            }
            hook = await channel.createWebhook({
                name: NAME,
                avatar: client.user.displayAvatarURL(),
            });
        }
        
        const successMsg = `[${channel.name}] Processed channel: ${channel.name}`;
        ConsoleLogger.success('Setup', successMsg);
        if (logCallback) logCallback(successMsg);

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
    const channelsArray = Array.from(targetChannels.values());
    const CONCURRENT_LIMIT = 5; // Process 5 channels at a time
    
    // Process single channel with retry logic
    const processChannel = async (channel, index) => {
        let attempts = 0;
        let success = false;
        let result = null;
        
        while (!success && attempts < 3) {
            attempts++;
            try {
                if (attempts === 1) {
                    const msg = `[${category.name}] Processing channel ${index + 1}/${channelsArray.length}: ${channel.name}`;
                    if (logCallback) logCallback(msg);
                }

                const existingHooks = await withTimeout(channel.fetchWebhooks(), 20000);
                let hook = existingHooks.find(h => h.owner.id === client.user.id && h.name === NAME);

                if (!hook) {
                    if (existingHooks.size >= 10) {
                        const warning = `[${category.name}] ⚠️ Channel '${channel.name}' has 10 webhooks and none belong to us. Skipping.`;
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
                
                if (hook) {
                    result = { hook, channelName: channel.name };
                    const successMsg = `[${category.name}] Processed channel ${index + 1}/${channelsArray.length}: ${channel.name}`;
                    ConsoleLogger.success('Setup', successMsg);
                    if (logCallback) logCallback(successMsg);
                }
                success = true;
            } catch (err) {
                if (err.status === 429 || err.code === 429) {
                    let retryTime = 5000;
                    if (err.retry_after) retryTime = err.retry_after * 1000;
                    if (err.global) retryTime += 100;
                    
                    ConsoleLogger.warn('Setup', `[${category.name}] ⚠️ Rate Limit on '${channel.name}'. Waiting ${(retryTime/1000).toFixed(1)}s (Attempt ${attempts}/3)...`);
                    if (logCallback) logCallback(`[${category.name}] ⚠️ Rate Limit. Retrying in ${(retryTime/1000).toFixed(1)}s...`);
                    
                    await sleep(retryTime);
                    continue;
                }

                if (err.message === 'Timeout') {
                    ConsoleLogger.warn('Setup', `[${category.name}] ⚠️ Timeout on '${channel.name}'. Retrying (Attempt ${attempts}/3)...`);
                    if (logCallback) logCallback(`[${category.name}] ⚠️ Timeout. Retrying...`);
                    await sleep(2000);
                    continue;
                }

                const errName = err.code === 50013 ? 'Missing Permissions' : err.message;
                const errorMsg = `[${category.name}] ❌ Failed to setup channel '${channel.name}': ${errName}. Skipping.`;
                ConsoleLogger.error('Setup', errorMsg);
                if (logCallback) logCallback(errorMsg);
                success = true;
            }
        }
        
        return result;
    };
    
    // Process channels in batches
    for (let i = 0; i < channelsArray.length; i += CONCURRENT_LIMIT) {
        const batch = channelsArray.slice(i, Math.min(i + CONCURRENT_LIMIT, channelsArray.length));
        const batchResults = await Promise.allSettled(
            batch.map((channel, batchIndex) => processChannel(channel, i + batchIndex))
        );
        
        // Collect successful results
        for (const result of batchResults) {
            if (result.status === 'fulfilled' && result.value) {
                hooks.push(result.value);
            }
        }
        
        // Small delay between batches to avoid rate limits
        if (i + CONCURRENT_LIMIT < channelsArray.length) {
            await sleep(100);
        }
    }
    
    return hooks;
}

/**
 * Helper to prepare threads for a single channel
 * @param {import('discord.js').TextChannel} channel 
 * @param {import('discord.js').Client} client 
 * @param {Function} logCallback 
 * @returns {Promise<Array>}
 */
async function prepareThreadsForChannel(channel, client, logCallback) {
    try {
        const msg = `[${channel.name}] Preparing thread...`;
        if (logCallback) logCallback(msg);
        
        const thread = await findOrCreateThread(channel, client);
        
        if (!thread) {
            const warning = `[${channel.name}] ⚠️ Could not create/find thread. Skipping.`;
            ConsoleLogger.warn('Setup', warning);
            if (logCallback) logCallback(warning);
            return [];
        }
        
        const successMsg = `[${channel.name}] Thread ready: ${thread.name}`;
        ConsoleLogger.success('Setup', successMsg);
        if (logCallback) logCallback(successMsg);
        
        return [{ threadId: thread.id, channelId: channel.id, threadName: thread.name }];
    } catch (err) {
        ConsoleLogger.error('Setup', `Failed to setup thread for '${channel.name}':`, err);
        if (logCallback) logCallback(`[Setup] ❌ Thread failed: ${err.message}`);
        return [];
    }
}

/**
 * Helper to prepare threads for all channels in a category
 * @param {import('discord.js').CategoryChannel} category 
 * @param {import('discord.js').Client} client 
 * @param {Function} logCallback 
 * @returns {Promise<Array>}
 */
async function prepareThreadsForCategory(category, client, logCallback) {
    const targetChannels = category.children.cache.filter(c => c.type === ChannelType.GuildText);
    if (targetChannels.size === 0) throw new Error('No text channels found.');
    
    const threads = [];
    const channelsArray = Array.from(targetChannels.values());
    const CONCURRENT_LIMIT = 5; // Process 5 channels at a time
    
    // Process single channel
    const processChannel = async (channel, index) => {
        try {
            const msg = `[${category.name}] Preparing thread ${index + 1}/${channelsArray.length}: ${channel.name}`;
            if (logCallback) logCallback(msg);
            
            const thread = await findOrCreateThread(channel, client);
            
            if (!thread) {
                const warning = `[${category.name}] ⚠️ Could not create/find thread for '${channel.name}'. Skipping.`;
                ConsoleLogger.warn('Setup', warning);
                if (logCallback) logCallback(warning);
                return null;
            }
            
            const successMsg = `[${category.name}] Thread ready ${index + 1}/${channelsArray.length}: ${thread.name}`;
            ConsoleLogger.success('Setup', successMsg);
            if (logCallback) logCallback(successMsg);
            
            return { threadId: thread.id, channelId: channel.id, threadName: thread.name };
        } catch (err) {
            const errorMsg = `[${category.name}] ❌ Failed to setup thread for '${channel.name}': ${err.message}`;
            ConsoleLogger.error('Setup', errorMsg);
            if (logCallback) logCallback(errorMsg);
            return null;
        }
    };
    
    // Process channels in batches
    for (let i = 0; i < channelsArray.length; i += CONCURRENT_LIMIT) {
        const batch = channelsArray.slice(i, Math.min(i + CONCURRENT_LIMIT, channelsArray.length));
        const batchResults = await Promise.allSettled(
            batch.map((channel, batchIndex) => processChannel(channel, i + batchIndex))
        );
        
        // Collect successful results
        for (const result of batchResults) {
            if (result.status === 'fulfilled' && result.value) {
                threads.push(result.value);
            }
        }
        
        // Small delay between batches to avoid rate limits
        if (i + CONCURRENT_LIMIT < channelsArray.length) {
            await sleep(100);
        }
    }
    
    return threads;
}

/**
 * Start loops for all configured channels
 */
async function startLoops(interaction, targetId) {
    if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
        return interaction.reply({ content: '❌ **Access Denied.** Admin only.', flags: MessageFlags.Ephemeral });
    }

    if (configuredChannels.size === 0) {
        return interaction.reply({ content: '❌ No channels configured! Use \`/debug webhook-looper set\` first.', flags: MessageFlags.Ephemeral });
    }

    let channelsToRun;
    if (targetId === 'all' || !targetId) {
        channelsToRun = Array.from(configuredChannels.entries()).filter(([id]) => !activeLoops.has(id));
        if (channelsToRun.length === 0) {
            return interaction.reply({ content: '⚠️ All configured loops are already running!', flags: MessageFlags.Ephemeral });
        }
    } else {
        if (activeLoops.has(targetId)) {
            const data = configuredChannels.get(targetId);
            return interaction.reply({ content: `⚠️ Loop for **${data?.config.channelName || targetId}** is already running!`, flags: MessageFlags.Ephemeral });
        }
        const data = configuredChannels.get(targetId);
        if (!data) {
            return interaction.reply({ content: '❌ Configuration not found!', flags: MessageFlags.Ephemeral });
        }
        channelsToRun = [[targetId, data]];
    }

    await interaction.deferReply();
    await interaction.editReply({ content: '⏳ Starting webhook looper...' });
    const message = await interaction.fetchReply();
    const showLogs = interaction.options.getBoolean('logs') ?? false;
    
    // Get interval parameter (optional, defaults to 0 = infinite random mode)
    const intervalInput = interaction.options.getString('interval') ?? "0";
    let interval;
    try {
        interval = parseInterval(intervalInput);
    } catch (err) {
        return interaction.reply({ content: `❌ ${err.message}`, flags: MessageFlags.Ephemeral });
    }

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
             components.push(V2Builder.textDisplay(`\`\`\`ansi\n${logContent}\n\`\`\``));
        } else {
             components.push(V2Builder.textDisplay(`Starting up...`));
        }

        const container = V2Builder.container(components);

        await message.edit({ 
            content: null,
            components: [container],
            flags: MessageFlags.IsComponentsV2
        }).catch(err => ConsoleLogger.error('WebhookLooper', 'UI Update Error:', err));
    };

    const logCallback = (msg, force = false) => {
        if (logs.length >= 50) logs.shift();
        logs.push(msg);
        updateUI(force);
    };

    let totalChannels = 0;
    for (const [_, data] of channelsToRun) {
        if (data.hooks) totalChannels += data.hooks.length;
    }
    const configCount = channelsToRun.length;

    const startMsg = `🚀 Configs: ${configCount} | Channels: ${totalChannels}`;
    ConsoleLogger.success('WebhookLooper', startMsg);
    logs.push(startMsg);
    await updateUI(true);

    const startTime = Date.now();

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
        
        // Load thread data if enabled
        if (data.config.useThread && !data.threads) {
            ConsoleLogger.info(data.config.channelName, `Loading threads...`);
            logCallback(`[${data.config.channelName}] Loading threads...`, true);
            
            data.threads = await loadThreadsForConfig(id, data, interaction.client);
            configuredChannels.set(id, data);
            
            ConsoleLogger.success(data.config.channelName, `Threads loaded: ${data.threads.length}`);
            logCallback(`[${data.config.channelName}] Threads loaded: ${data.threads.length}`, true);
        }
        
        // Apply the runtime interval to this loop's config
        data.config.interval = interval;
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
        // Success - no logging needed
    } else {
        const msg = `⚠️ Finished execution but no channels were targeted.`;
        ConsoleLogger.warn('WebhookLooper', msg);
        if (logCallback) logCallback(`[${title('WebhookLooper')}] ${msg}`, true);
    }
}

/**
 * Stop a specific loop and perform cleanup
 * @param {string} channelId 
 * @param {import('discord.js').Client} client
 */
async function stopLoopInternal(channelId, client) {
    const state = activeLoops.get(channelId);
    if (!state) return false;

    const data = configuredChannels.get(channelId);
    if (!data) return false;

    // 1. Signal stop (interrupts round loop)
    state.stop();

    // 2. Clear timeout (interrupts wait between rounds in random mode)
    if (state.intervalTimeout) {
        clearTimeout(state.intervalTimeout);
        state.intervalTimeout = null;
    }

    // 3. Persist state
    webhookRepo.setLoopState(channelId, false);
    activeLoops.delete(channelId);

    // 4. Rename to inactive
    if (data.config.inactiveChannelName) {
        const channel = await client.channels.fetch(channelId).catch(() => null);
        if (channel) {
            await renameChannel(channel, data.config.inactiveChannelName);
        }
    }

    ConsoleLogger.warn('WebhookLooper', `Stopped loop for: ${data.config.channelName}`);
    return true;
}

// Internal loop starter
function startLoop(channelId, data, interaction, logCallback, startTime, executionState) {
    let running = true;
    const stop = () => { 
        running = false;
        // If we are in a sleep, we need to be able to wake up or at least check frequently
    };
    
    const state = { 
        stop, 
        roundsTotal: 0, 
        currentRound: 0, 
        intervalTimeout: null,
        // Helper to check if still running (checks both local flag and global map)
        isAlive: () => running && activeLoops.has(channelId)
    };
    activeLoops.set(channelId, state);
    webhookRepo.setLoopState(channelId, true); // Persist state

    const log = (msg, force = false) => {
        const currentName = data.config.channelName;
        if (executionState?.isInitializing) {
            ConsoleLogger.info('Startup', `[${currentName}] ${msg}`);
        } else {
            ConsoleLogger.info(currentName, msg);
        }
        // Prefix for UI logs to stay distinguishable in global view
        if (logCallback) logCallback(`[${title(currentName)}] ${msg}`, force);
    };

    let gracefulStop = false; // Flag for finishing last round after timeout

    // NEW LOGIC:
    // If interval > 0: Loop continuously until interval time expires (ignore rounds)
    // If interval = 0: Infinite random mode (1-100 rounds, 1-10min delays)
    
    const isTimedMode = data.config.interval > 0;
    const isRandomInfiniteMode = data.config.interval === 0;
    
    if (isTimedMode) {
        // Timed mode: loop continuously until interval expires
        log(`Starting timed loop for ${formatInterval(data.config.interval)}`, true);
        
        state.intervalTimeout = setTimeout(async () => {
            log(`⏰ Time limit reached. Finishing last round...`, true);
            gracefulStop = true;
            // We do NOT call stop() here. We let the loop finish its current iteration.
        }, data.config.interval);
    } else if (isRandomInfiniteMode) {
        // Random infinite mode
        log(`Starting infinite random mode`, true);
    }

    (async () => {
        // Record activity to wake bot from idle when starting loop
        statusRotator.recordActivity(interaction.client);
        
        // Rename channel to active name BEFORE starting the loop
        let channel = await interaction.client.channels.fetch(channelId).catch(() => null);
        if (channel && data.config.activeChannelName) {
            await renameChannel(channel, data.config.activeChannelName);
        }

        const isAlive = state.isAlive;
        
        while (isAlive()) {
            if (isRandomInfiniteMode) {
                // Generate random rounds and delay for this iteration
                const randomRounds = Math.floor(Math.random() * 100) + 1; // 1-100
                const randomDelay = (Math.floor(Math.random() * 10) + 1) * 60 * 1000; // 1-10 minutes in ms
                
                state.roundsTotal = randomRounds;
                state.currentRound = 0;
                
                log(`🎲 Random: ${randomRounds} rounds, next delay: ${formatInterval(randomDelay)}`, true);
                
                // Execute the random rounds
                for (let i = 0; i < randomRounds && isAlive(); i++) {
                    state.currentRound = i + 1;
                    log(`Round ${state.currentRound}/${randomRounds}`, true);
                    
                    await executeRound(data, isAlive, log, interaction);
                    
                    if (!isAlive()) break;
                    if (i < randomRounds - 1) {
                        await sleep(LOOP_DELAY);
                    }
                }
                
                // After completing rounds, random chance to enter continuous run mode
                const randomChance = Math.random(); // Random chance 0-100%
                if (isAlive() && Math.random() < randomChance) {
                    const continuousRunDuration = (Math.floor(Math.random() * 10) + 1) * 60 * 1000; // 1-10 minutes
                    log(`🎰 Bonus: Running continuously for ${formatInterval(continuousRunDuration)}`, true);
                    
                    const continuousStartTime = Date.now();
                    let continuousRoundCount = 0;
                    
                    while (isAlive() && (Date.now() - continuousStartTime) < continuousRunDuration) {
                        continuousRoundCount++;
                        log(`Continuous Round ${continuousRoundCount}`, true);
                        
                        await executeRound(data, isAlive, log, interaction);
                        
                        if (isAlive() && (Date.now() - continuousStartTime) < continuousRunDuration) {
                            await sleep(LOOP_DELAY);
                        }
                    }
                    
                    log(`✅ Completed continuous run (${continuousRoundCount} rounds)`, true);
                }
                
                // Wait random delay before next iteration
                if (isAlive()) {
                    log(`⏳ ${val(`Waiting ${formatInterval(randomDelay)} before next iteration...`)}`, true);
                    
                    // Use a promise-based sleep that we can "interrupt" if needed, 
                    // though for now clearing the timeout in stopLoopInternal and checking isAlive is enough
                    // because we split long sleeps into smaller chunks or just check after sleep.
                    // For now, let's keep it simple: if stopped during sleep, the loop condition while(isAlive()) will catch it.
                    await sleep(randomDelay);
                }
            } else if (isTimedMode) {
                // Timed mode: run until gracefulStop is set (or manual stop)
                // The loop condition while(isAlive()) catches manual stops.
                // We check gracefulStop inside.
                
                if (gracefulStop || !isAlive()) break;

                state.currentRound++;
                log(`Round ${val(state.currentRound)}`, true);
                
                await executeRound(data, isAlive, log, interaction);
                
                if (isAlive() && !gracefulStop) {
                    await sleep(LOOP_DELAY);
                }
            } else {
                // Fallback: use rounds parameter (old behavior for compatibility)
                const roundsTotal = data.config.rounds === 0 ? Math.floor(Math.random() * 10) + 1 : data.config.rounds;
                state.roundsTotal = roundsTotal;
                
                while (isAlive() && state.currentRound < roundsTotal) {
                    state.currentRound++;
                    log(`Round ${val(state.currentRound + '/' + roundsTotal)}`, true);
                    
                    await executeRound(data, isAlive, log, interaction);
                    
                    if (state.currentRound < roundsTotal && isAlive()) {
                        await sleep(LOOP_DELAY);
                    }
                }
                break; // Exit main while loop after rounds complete
            }
        }
        
        // Clear interval timeout if set
        if (state.intervalTimeout) {
            clearTimeout(state.intervalTimeout);
        }
        
        // Rename to inactive
        channel = await interaction.client.channels.fetch(channelId).catch(() => null);
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
async function executeRound(data, isAlive, log, interaction) {
    // Record activity to wake bot from idle
    statusRotator.recordActivity(interaction.client);
    
    // Send to channels via webhooks
    for (let i = 0; i < data.hooks.length; i += BATCH_SIZE) {
        if (!isAlive()) break;

        const batch = data.hooks.slice(i, i + BATCH_SIZE);
        await Promise.all(batch.map(async ({ hook, channelName }) => {
            if (!isAlive()) return;

            try {
                const content = data.config.message || '@everyone';
                
                // Extract role IDs from content for proper mention notifications
                const roleMatches = content.matchAll(/<@&(\d+)>/g);
                const roleIds = Array.from(roleMatches, m => m[1]);
                
                // Extract user IDs from content (exclude role mentions with &)
                const userMatches = content.matchAll(/<@!(\d+)>|<@(?!&)(\d+)>/g);
                const userIds = Array.from(userMatches, m => m[1] || m[2]);
                
                await hook.send({
                    content: content,
                    username: data.config.webhookAuthor || 'LoopHook',
                    avatarURL: data.config.webhookAvatar || interaction.client.user.displayAvatarURL(),
                    allowedMentions: {
                        parse: content.includes('@everyone') || content.includes('@here') ? ['everyone'] : [],
                        roles: roleIds,
                        users: userIds
                    }
                });
            } catch (err) {
                if (err.status === 429 || err.code === 429) {
                    let retryTime = 5000;
                    if (err.rawError && err.rawError.retry_after) {
                        retryTime = (err.rawError.retry_after * 1000) + 50;
                    }
                    
                    ConsoleLogger.warn(`Run-${data.config.channelName}`, `⚠️ Rate Limit hit on ${channelName}. Backing off ${val((retryTime/1000).toFixed(1) + 's')}...`);
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
        
        if (!isAlive()) break;
        await sleep(0);
    }
    
    // Send to threads if enabled
    if (data.config.useThread && data.threads && data.threads.length > 0) {
        for (let i = 0; i < data.threads.length; i += BATCH_SIZE) {
            if (!isAlive()) break;
            
            const batch = data.threads.slice(i, i + BATCH_SIZE);
            await Promise.all(batch.map(async ({ hook, threadId, threadName }) => {
                if (!isAlive()) return;
                
                try {
                    const content = data.config.threadMessage || data.config.message || '@everyone';
                    
                    // Extract role IDs from content for proper mention notifications
                    const roleMatches = content.matchAll(/<@&(\d+)>/g);
                    const roleIds = Array.from(roleMatches, m => m[1]);
                    
                    // Extract user IDs from content (exclude role mentions with &)
                    const userMatches = content.matchAll(/<@!(\d+)>|<@(?!&)(\d+)>/g);
                    const userIds = Array.from(userMatches, m => m[1] || m[2]);
                    
                    await hook.send({
                        content: content,
                        username: data.config.webhookAuthor || 'LoopHook',
                        avatarURL: data.config.webhookAvatar || interaction.client.user.defaultAvatarURL(),
                        threadId: threadId,
                        allowedMentions: {
                            parse: content.includes('@everyone') || content.includes('@here') ? ['everyone'] : [],
                            roles: roleIds,
                            users: userIds
                        }
                    });
                } catch (err) {
                    if (err.status === 429 || err.code === 429) {
                        let retryTime = 5000;
                        if (err.rawError && err.rawError.retry_after) {
                            retryTime = (err.rawError.retry_after * 1000) + 50;
                        }
                        
                        ConsoleLogger.warn(`Run-${data.config.channelName}`, `⚠️ Rate Limit hit on thread ${threadName}. Backing off ${val((retryTime/1000).toFixed(1) + 's')}...`);
                        await sleep(retryTime);
                    } else if (err.code === 10015) {
                        ConsoleLogger.warn(`Run-${data.config.channelName}`, `⚠️ Webhook for thread ${threadName} is missing (404). Removing from list.`);
                        const idx = data.threads.findIndex(t => t.threadId === threadId);
                        if (idx > -1) data.threads.splice(idx, 1);
                    } else if (err.code === 10003) {
                        // Thread is archived or deleted
                        ConsoleLogger.warn(`Run-${data.config.channelName}`, `⚠️ Thread ${threadName} not found (archived/deleted). Removing from list.`);
                        const idx = data.threads.findIndex(t => t.threadId === threadId);
                        if (idx > -1) data.threads.splice(idx, 1);
                    } else {
                        ConsoleLogger.error(`Run-${data.config.channelName}`, `Failed to send to thread ${threadName}:`, err);
                    }
                }
            }));
            
            if (!isAlive()) break;
            await sleep(0);
        }
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
        const { id, config } = activeConfigs[0];
        await stopLoopInternal(id, interaction.client);
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
                webhookAuthor: row.webhookAuthor,
                webhookAvatar: row.webhookAvatar,
                useThread: row.useThread === 1,
                threadMessage: row.threadMessage,
                isRunning: row.isRunning === 1
            },
            hooks: null, // Lazy load
            threads: null // Lazy load
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
            
            // Load threads if enabled
            if (data.config.useThread && !data.threads) {
                try {
                    data.threads = await loadThreadsForConfig(id, data, client);
                    configuredChannels.set(id, data);
                    ConsoleLogger.success('WebhookLooper', `Loaded ${data.threads.length} thread(s) for ${data.config.channelName}`);
                } catch (e) {
                    ConsoleLogger.error('WebhookLooper', `Failed to load threads for ${data.config.channelName}:`, e);
                    data.threads = [];
                }
            }
            
            startLoop(id, data, mockInteraction, null, startTime, { isInitializing: true });
        }
    }
    
    // Sync base names (updates DB if manual renames happened while inactive)
    await syncBaseNames(client);
    
    // Ensure all channels are in inactive state on startup (Batched for performance)
    // Skip channels that are currently running (they will be renamed to active by startLoop)
    const updates = [];
    for (const [channelId, data] of configuredChannels) {
        // Only rename to inactive if not currently in activeLoops
        if (data.config.inactiveChannelName && !activeLoops.has(channelId)) {
            updates.push({ channelId, name: data.config.inactiveChannelName });
        }
    }

    if (updates.length > 0) {
        ConsoleLogger.info('WebhookLooper', `Ensuring ${updates.length} inactive channels are properly named...`);
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
    stopLoops,
    stopLoopInternal,
    activeLoops
};
