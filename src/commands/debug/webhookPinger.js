const { setPingCategory, runPingCategories, listPingCategories, resetPingCategories } = require('../../scripts/webhookPinger');

module.exports = {
    async handle(interaction) {
        const subcommand = interaction.options.getSubcommand();

        if (subcommand === 'list') {
            await listPingCategories(interaction);
        } else if (subcommand === 'reset') {
            await resetPingCategories(interaction);
        } else if (subcommand === 'set') {
            await setPingCategory(interaction);
        } else if (subcommand === 'run') {
            await runPingCategories(interaction);
        }
    }
};
