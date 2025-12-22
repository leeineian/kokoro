const { MessageFlags } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const { setLoopConfig, startLoops, listLoopConfigs, stopLoops, stopLoopInternal, activeLoops } = require('../../daemons/webhookLooper');
const { formatInterval, val, title } = require('./.helper');


/**
 * Debug Webhook Looper Handler - Manages webhook stress testing and looping
 * Allows configuration and execution of webhook loops for testing
 */
module.exports = {
    /**
     * Autocomplete handler for webhook-looper options
     */
    async autocomplete(interaction) {
        const focusedValue = interaction.options.getFocused().toLowerCase();
        
        // Only handle stop subcommand for now
        if (interaction.options.getSubcommand() !== 'stop') return;

        const options = [];
        
        // Add "Stop All" if multiple loops are running
        if (activeLoops.size > 1) {
            options.push({ name: '🛑 Stop All Running Loops', value: 'all' });
        }

        // Add individual active loops
        for (const [id, state] of activeLoops) {
            const { webhookLooper: repo } = require('../../utils/core/database');
            const config = repo.getLoopConfig(id);
            const name = config?.channelName || id;
            options.push({ name: `🛑 Stop Loop: ${name}`, value: id });
        }

        // Filter by user input
        const filtered = options.filter(opt => opt.name.toLowerCase().includes(focusedValue)).slice(0, 25);
        
        await interaction.respond(filtered).catch(() => {});
    },

    /**
     * Handles webhook looper subcommands
     * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
     * @returns {Promise<void>}
     */
    async handle(interaction) {
        try {
            const subcommand = interaction.options.getSubcommand();

            switch (subcommand) {
                case 'list':
                    return await listLoopConfigs(interaction);
                case 'set':
                    return await setLoopConfig(interaction);
                case 'start':
                    return await startLoops(interaction);
                case 'stop':
                    const target = interaction.options.getString('target');
                    if (target) {
                        return await this.handleStopTarget(interaction, target);
                    }
                    return await stopLoops(interaction);
                case 'purge':
                    return await this.purgeWebhooks(interaction);
                default:
                    ConsoleLogger.warn('WebhookLooper', `Unknown subcommand: ${subcommand}`);
                    return interaction.reply({
                        content: 'Unknown webhook looper command! 🔧',
                        flags: MessageFlags.Ephemeral
                    });
            }
        } catch (error) {
            ConsoleLogger.error('WebhookLooper', 'Failed to handle webhook looper command:', error);
            
            if (!interaction.replied && !interaction.deferred) {
                await interaction.reply({
                    content: 'Failed to process webhook looper command! 🔧',
                    flags: MessageFlags.Ephemeral
                });
            }
        }
    },

    /**
     * Purge all webhooks from a category
     */
    async purgeWebhooks(interaction) {
        const { PermissionFlagsBits, ChannelType } = require('discord.js');
        const V2Builder = require('../../utils/core/components');
        
        if (!interaction.member.permissions.has(PermissionFlagsBits.Administrator)) {
            return interaction.reply({ content: '❌ **Access Denied.** Admin only.', flags: MessageFlags.Ephemeral });
        }

        const category = interaction.options.getChannel('category');

        if (category.type !== ChannelType.GuildCategory) {
            return interaction.reply({ content: '❌ Please select a category.', flags: MessageFlags.Ephemeral });
        }

        await interaction.deferReply({ flags: MessageFlags.Ephemeral });
        
        const textChannels = category.children.cache.filter(c => c.type === ChannelType.GuildText);
        
        if (textChannels.size === 0) {
            return interaction.editReply({ content: `ℹ️ No text channels in **${category.name}**.` });
        }

        const logs = [];
        let totalDeleted = 0;
        
        const updateUI = async () => {
            const logContent = logs.slice(-20).join('\n');
            const container = V2Builder.container([
                V2Builder.textDisplay(`🗑️ **Purging Webhooks from ${category.name}**\n\n\`\`\`log\n${logContent}\n\`\`\``)
            ]);
            
            await interaction.editReply({
                content: null,
                components: [container],
                flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral
            }).catch(() => {});
        };

        const log = (msg) => {
            logs.push(msg);
            ConsoleLogger.info('WebhookPurge', msg);
        };

        log(`Starting purge for category: ${category.name} (${textChannels.size} channels)`);
        await updateUI();

        const sleep = (ms) => new Promise(resolve => setTimeout(resolve, ms));

        for (const [_, channel] of textChannels) {
            try {
                log(`[${channel.name}] Fetching webhooks...`);
                await updateUI();
                
                const webhooks = await channel.fetchWebhooks();
                
                if (webhooks.size === 0) {
                    log(`[${channel.name}] No webhooks found`);
                    await updateUI();
                    continue;
                }
                
                log(`[${channel.name}] Found ${webhooks.size} webhook(s)`);
                await updateUI();
                
                for (const [_, webhook] of webhooks) {
                    let attempts = 0;
                    let deleted = false;
                    
                    while (!deleted && attempts < 3) {
                        attempts++;
                        try {
                            await webhook.delete();
                            totalDeleted++;
                            log(`[${channel.name}] Deleted: ${webhook.name}`);
                            await updateUI();
                            deleted = true;
                            
                            // Small delay between deletions
                            await sleep(10);
                        } catch (err) {
                            if (err.status === 429 || err.code === 429) {
                                const retryAfter = err.retry_after ? err.retry_after * 1000 : 5000;
                                log(`[${channel.name}] ⚠️ Rate limited. Waiting ${(retryAfter/1000).toFixed(1)}s...`);
                                await updateUI();
                                await sleep(retryAfter);
                            } else {
                                log(`[${channel.name}] ❌ Failed to delete ${webhook.name}: ${err.message}`);
                                await updateUI();
                                deleted = true; // Skip this one
                            }
                        }
                    }
                }
                
            } catch (err) {
                log(`[${channel.name}] ❌ Error: ${err.message}`);
                await updateUI();
            }
            
            // Delay between channels
            await sleep(50);
        }

        log(`✅ Purge complete! Deleted ${totalDeleted} webhook(s).`);
        await updateUI();

        // Final summary
            await interaction.editReply({
                content: `✅ **Purge Complete**\n\nDeleted **${val(totalDeleted)}** webhook(s) from **${val(category.name)}**.`,
                components: []
            });
    },

    /**
     * Handle stopping a specific target from autocomplete
     */
    async handleStopTarget(interaction, target) {
        if (target === 'all') {
            const loopIds = Array.from(activeLoops.keys());
            
            if (loopIds.length === 0) {
                return interaction.reply({ content: 'ℹ️ No loops are currently running.', flags: MessageFlags.Ephemeral });
            }

            for (const id of loopIds) {
                await stopLoopInternal(id, interaction.client);
            }

            return interaction.reply({ 
                content: `🛑 Stopped all **${val(loopIds.length)}** running loops.`, 
                flags: MessageFlags.Ephemeral 
            });
        } else {
            // Stop specific loop
            const success = await stopLoopInternal(target, interaction.client);
            
            if (!success) {
                return interaction.reply({ 
                    content: `❌ Could not find or stop loop for target: \`${target}\`.`, 
                    flags: MessageFlags.Ephemeral 
                });
            }

            return interaction.reply({ 
                content: `✅ Stopped the selected loop.`, 
                flags: MessageFlags.Ephemeral 
            });
        }
    },

    // Component handlers for interactive elements
    handlers: {
        /**
         * Handle loop config deletion from select menu
         */
        delete_loop_config: async (interaction) => {
            const channelId = interaction.values[0];
            const { webhookLooper: repo } = require('../../utils/core/database');
            const V2Builder = require('../../utils/core/components');
            
            try {
                // Get config name before deletion
                const config = repo.getLoopConfig(channelId);
                const configName = config?.channelName || 'Unknown';
                
                // Delete from database
                repo.deleteLoopConfig(channelId);
                
                ConsoleLogger.info('WebhookLooper', `Deleted config for: ${configName}`);
                
                // Get remaining configs and rebuild list
                const remainingConfigs = repo.getAllLoopConfigs();
                
                if (remainingConfigs.length === 0) {
                    // No more configs, show simple message with V2 component
                    const container = V2Builder.container([
                        V2Builder.textDisplay(`✅ Deleted configuration for **${configName}**.\n\nℹ️ No configurations remaining.`)
                    ]);
                    
                    await interaction.update({
                        content: null,
                        components: [container],
                        flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral
                    });
                } else {
                    // Rebuild the list with remaining configs
                    const list = remainingConfigs.map(cfg => {
                        const typeIcon = cfg.channelType === 'category' ? '📁' : '💬';
                        const roundsStr = cfg.rounds === 0 ? 'Random' : cfg.rounds;
                        const intervalStr = formatInterval(cfg.interval);
                        
                        return `${typeIcon} **${cfg.channelName}** - Rounds: ${roundsStr}, Interval: ${intervalStr}`;
                    }).join('\n\n');
                    
                    const selectOptions = remainingConfigs.map(cfg => ({
                        label: cfg.channelName,
                        value: cfg.channelId,
                        description: `${cfg.channelType} • Rounds: ${cfg.rounds === 0 ? 'Random' : cfg.rounds}`,
                        emoji: cfg.channelType === 'category' ? '📁' : '💬'
                    }));
                    
                    const components = [
                        V2Builder.textDisplay(`✅ Deleted **${configName}**\n\n**Remaining Configurations:**\n${list}`),
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
                    
                    await interaction.update({
                        content: null,
                        components: [container],
                        flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral
                    });
                }
                
            } catch (error) {
                ConsoleLogger.error('WebhookLooper', 'Failed to delete config:', error);
                
                if (!interaction.replied && !interaction.deferred) {
                    await interaction.reply({
                        content: '❌ Failed to delete configuration.',
                        flags: MessageFlags.Ephemeral
                    });
                }
            }
        },

        /**
         * Handle stopping loops from select menu  
         */
        stop_loop_select: async (interaction) => {
            const V2Builder = require('../../utils/core/components');
            const selection = interaction.values[0];

            try {
                if (selection === '__STOP_ALL__') {
                    const { activeLoops } = require('../../daemons/webhookLooper');
                    const loopIds = Array.from(activeLoops?.keys() || []);
                    
                    if (loopIds.length === 0) {
                         return interaction.update({
                            content: null,
                            components: [V2Builder.container([V2Builder.textDisplay('ℹ️ No loops are currently running.')])],
                            flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral
                        });
                    }

                    for (const id of loopIds) {
                        await stopLoopInternal(id, interaction.client);
                    }

                    const container = V2Builder.container([
                        V2Builder.textDisplay(`🛑 **Action Complete**\n\nStopped all **${val(loopIds.length)}** running loops.`),
                    ]);

                    await interaction.update({
                        content: null,
                        components: [container],
                        flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral
                    });
                } else {
                    // Stop specific loop
                    const success = await stopLoopInternal(selection, interaction.client);
                    
                    const container = V2Builder.container([
                        V2Builder.textDisplay(success ? `✅ **Action Complete**\n\nStopped the selected loop.` : `❌ **Error**\n\nCould not find or stop the selected loop.`),
                    ]);

                    await interaction.update({
                        content: null,
                        components: [container],
                        flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral
                    });
                }
            } catch (error) {
                ConsoleLogger.error('WebhookLooper', 'Failed to handle stop loop selection:', error);
                
                try {
                     await interaction.reply({
                        content: '❌ Failed to process selection.',
                        flags: MessageFlags.Ephemeral
                     });
                } catch (e) {}
            }
        }
    }
};
