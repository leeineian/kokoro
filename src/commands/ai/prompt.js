const { MessageFlags } = require('discord.js');
const userPrompts = require('../../utils/db/repo/aiPrompts');
const { DEFAULT_SYSTEM_PROMPT } = require('../../configs/ai');

module.exports = {
    async handle(interaction) {
        const subcommand = interaction.options.getSubcommand();
        const userId = interaction.user.id;

        if (subcommand === 'set') {
            const text = interaction.options.getString('text');
            if (text.length > 1000) return interaction.reply({ content: '❌ Too long!', flags: MessageFlags.Ephemeral });
            
            userPrompts.set(userId, text);
            return interaction.reply({ 
                content: `✅ **Custom Prompt Set!**\n> *${text}*`, 
                flags: MessageFlags.Ephemeral 
            });

        } else if (subcommand === 'reset') {
            userPrompts.delete(userId);
            return interaction.reply({ 
                content: `🔄 **Prompt Reset.**\n> Default: *${DEFAULT_SYSTEM_PROMPT}*`, 
                flags: MessageFlags.Ephemeral 
            });

        } else if (subcommand === 'view') {
            const prompt = userPrompts.get(userId) || DEFAULT_SYSTEM_PROMPT;
            return interaction.reply({ 
                content: `**Your Prompt:**\n> ${prompt}`, 
                flags: MessageFlags.Ephemeral 
            });
        }
    }
};
