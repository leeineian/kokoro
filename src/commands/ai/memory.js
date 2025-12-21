const aiMemory = require('../../utils/db/repo/aiMemory');

module.exports = {
    async handle(interaction) {
        const subcommand = interaction.options.getSubcommand();
        
        if (subcommand === 'reset') {
            const channelId = interaction.channelId;
            aiMemory.reset(channelId);
            
            // This is NOT ephemeral, so everyone knows the context was wiped
            return interaction.reply({ 
                content: `🧠 **AI Memory Wiped.**\nI have forgotten everything said in this channel before this moment.` 
            });
        }
    }
};
