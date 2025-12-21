const { MessageFlags } = require('discord.js');
const V2Builder = require('../../utils/components');
const db = require('../../utils/database');
const ConsoleLogger = require('../../utils/consoleLogger');

module.exports = {
    async handle(interaction) {
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
            ConsoleLogger.error('Reminder', 'Failed to list reminders:', error);
            await interaction.reply({ 
                content: 'Database error.', 
                flags: MessageFlags.Ephemeral 
            });
        }
    }
};
