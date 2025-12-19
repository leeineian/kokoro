const { MessageFlags } = require('discord.js');
const db = require('../utils/database');
const V2Builder = require('../utils/components');

module.exports = {
    customId: 'clear_reminders',
    async execute(interaction) {
        try {
            const count = db.deleteAllReminders(interaction.user.id);
            
            const v2Container = V2Builder.container([
                V2Builder.textDisplay(`Cleared ${count} reminders.`)
            ]);

            await interaction.update({
                components: [v2Container]
                // Note: Flags should persist or be inferred from the interaction type/builder
            });
        } catch (error) {
            console.error('Failed to clear reminders:', error);
            await interaction.reply({ 
                content: 'Failed to clear reminders.', 
                flags: MessageFlags.Ephemeral 
            });
        }
    }
};
