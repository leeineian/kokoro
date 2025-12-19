const { SlashCommandBuilder, MessageFlags } = require('discord.js');
const chrono = require('chrono-node');
const V2Builder = require('../utils/components');
const db = require('../utils/database');

module.exports = {
	data: new SlashCommandBuilder()
		.setName('reminder')
		.setDescription('Manage your reminders')
        .addSubcommand(subcommand =>
            subcommand
                .setName('set')
                .setDescription('Set a new reminder')
                .addStringOption(option => 
                    option.setName('message')
                        .setDescription('What should I remind you about?')
                        .setRequired(true))
                .addStringOption(option => 
                    option.setName('when')
                        .setDescription('When? (e.g. "tomorrow at 9am", "in 30 mins")')
                        .setRequired(true))
                .addStringOption(option =>
                    option.setName('sendto')
                        .setDescription('Where should I send the reminder?')
                        .setRequired(false)
                        .addChoices(
                            { name: 'Direct Message (Default)', value: 'dm' },
                            { name: 'This Channel', value: 'channel' }
                        )))
        .addSubcommand(subcommand =>
            subcommand
                .setName('list')
                .setDescription('List your active reminders')),
	async execute(interaction, client) {
        const subcommand = interaction.options.getSubcommand();

        if (subcommand === 'set') {
            await this.handleSet(interaction);
        } else if (subcommand === 'list') {
            await this.handleList(interaction);
        }
	},

    async handleSet(interaction) {
        const message = interaction.options.getString('message');
        const when = interaction.options.getString('when');
        const sendTo = interaction.options.getString('sendto') || 'dm';

        const parsedDate = chrono.parseDate(when);
        if (!parsedDate) {
            return interaction.reply({ 
                content: 'I could not understand that time. Please try again (e.g. "in 10 minutes", "tomorrow at 5pm").', 
                flags: MessageFlags.Ephemeral 
            });
        }
        if (parsedDate <= new Date()) {
            return interaction.reply({ 
                content: 'That time is in the past! Please choose a future time.', 
                flags: MessageFlags.Ephemeral 
            });
        }
        const dueAt = parsedDate.getTime();

        const delayMs = dueAt - Date.now();

        // Defer immediately to allow time for DB and DM operations
        await interaction.deferReply({ flags: MessageFlags.Ephemeral });

        try {
            // Persist to DB
            const id = db.addReminder(interaction.user.id, interaction.channelId, message, dueAt, sendTo);

            // Schedule Delivery
            this.scheduleReminder(interaction.client, interaction.user.id, interaction.channelId, message, id, sendTo, dueAt);

            let dmUrl = null;
            let dmFailed = false;

            // DM Confirmation Logic
            if (sendTo === 'dm') {
                const v2Container = V2Builder.container([
                    V2Builder.textDisplay(
                        `📅 **Reminder Set**\nI'll remind you <t:${Math.floor(dueAt / 1000)}:R>.\nMessage: "${message}"`
                    ),
                    V2Builder.actionRow([
                        V2Builder.button('Dismiss', 'dismiss_message', 4)
                    ])
                ]);
                
                try {
                    const sentMsg = await interaction.user.send({ 
                        components: [v2Container],
                        flags: MessageFlags.IsComponentsV2
                    });
                    dmUrl = sentMsg.url;
                } catch (err) {
                    console.error(`DM Confirmation Failed (User: ${interaction.user.id}):`, err);
                    dmFailed = true;
                }
            }

            if (dmFailed) {
                 await interaction.editReply({ 
                    content: `⚠️ I could not send you a DM (Privacy Settings). Here is your confirmation:\n\n📅 **Reminder Set**\nI'll remind you <t:${Math.floor(dueAt / 1000)}:R>.\nMessage: "${message}"`
                });
            } else {
                const locationText = sendTo === 'channel' 
                    ? `<#${interaction.channelId}>` 
                    : (dmUrl ? `[your DMs](${dmUrl})` : 'your DMs');

                await interaction.editReply({
                    content: `Reminder set! I'll ping you in ${locationText}.`
                });
            }

        } catch (error) {
            console.error('Failed to set reminder:', error);
            await interaction.editReply({ 
                content: 'Failed to save reminder.'
            });
        }
    },

    async handleList(interaction) {
        try {
            // Synchronous call - no await
            const userReminders = db.getReminders(interaction.user.id);

            if (userReminders.length === 0) {
                return interaction.reply({ 
                    content: 'You have no confirmed reminders.', 
                    flags: MessageFlags.Ephemeral 
                });
            }

            const listText = userReminders.map((r, i) => 
                `${i+1}. "${r.message}" <t:${Math.floor(r.dueAt/1000)}:R> (${r.deliveryType === 'channel' && r.channelId ? `<#${r.channelId}>` : 'DM'})`
            ).join('\n');

            const v2Container = V2Builder.container([
                V2Builder.textDisplay(`**Your Reminders**\n${listText}`),
                V2Builder.actionRow([
                    V2Builder.button('Clear All Reminders', 'clear_reminders', 4) // Style 4 (Danger/Red)
                ])
            ]);
            
            // Ephemeral List
            await interaction.reply({
                flags: MessageFlags.IsComponentsV2 | MessageFlags.Ephemeral,
                components: [v2Container]
            });

        } catch (error) {
            console.error('Failed to list reminders:', error);
            await interaction.reply({ 
                content: 'Database error.', 
                flags: MessageFlags.Ephemeral 
            });
        }
    },

    scheduleReminder(client, userId, channelId, message, dbId, deliveryType, dueAt) {
        const now = Date.now();
        const delay = dueAt - now;
        const MAX_DELAY = 2147483647; // 2^31 - 1 (~24.8 days)

        if (delay <= 0) {
            // Send immediately
            this.sendReminder(client, userId, channelId, message, dbId, deliveryType);
        } else if (delay > MAX_DELAY) {
            // Too long for a single setTimeout, wait MAX_DELAY then check again
            setTimeout(() => {
                this.scheduleReminder(client, userId, channelId, message, dbId, deliveryType, dueAt);
            }, MAX_DELAY);
        } else {
            // Safe to schedule directly
            setTimeout(() => {
                this.sendReminder(client, userId, channelId, message, dbId, deliveryType);
            }, delay);
        }
    },

    // Helper to send and cleanup
    async sendReminder(client, userId, channelId, message, dbId, deliveryType = 'dm') {
        try {
            const reminderText = `⏰ **Time's Up, <@${userId}>!**\nReminder: "${message}"`;
            
            const v2Container = V2Builder.container([
                V2Builder.textDisplay(reminderText),
                V2Builder.actionRow([
                    V2Builder.button('Dismiss', 'dismiss_message', 4)
                ])
            ]);

            let deliverySuccess = false;

            if (deliveryType === 'channel' && channelId) {
                try {
                    const channel = await client.channels.fetch(channelId);
                    if (channel) {
                        await channel.send({ 
                            flags: MessageFlags.IsComponentsV2,
                            components: [v2Container] 
                        });
                        deliverySuccess = true;
                    }
                } catch (channelError) {
                    console.error(`Channel Delivery Failed (Channel: ${channelId}):`, JSON.stringify(channelError, null, 2));
                }
            } else {
                // DM Delivery
                try {
                    const user = await client.users.fetch(userId);
                    if (user) {
                         await user.send({ 
                            components: [v2Container],
                            flags: MessageFlags.IsComponentsV2
                        });
                        deliverySuccess = true;
                    }
                } catch (dmError) {
                    console.error(`DM Delivery Failed (User: ${userId}):`, dmError);
                    
                    // Fallback to channel if DM fails
                    if (channelId) {
                        try {
                            const channel = await client.channels.fetch(channelId);
                            if (channel) {
                                await channel.send({ 
                                    content: `<@${userId}> I couldn't DM you.`,
                                    flags: MessageFlags.IsComponentsV2,
                                    components: [v2Container] 
                                });
                                deliverySuccess = true;
                            }
                        } catch (channelError) {
                            console.error(`Channel Delivery Failed (Channel: ${channelId}):`, channelError);
                        }
                    }
                }
            }

            // Synchronous delete
            db.deleteReminder(dbId);

        } catch (error) {
            console.error(`Failed to deliver reminder ${dbId}:`, error);
        }
    }
};
