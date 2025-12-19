const { SlashCommandBuilder, MessageFlags, PermissionFlagsBits } = require('discord.js');

module.exports = {
	data: new SlashCommandBuilder()
		.setName('message')
		.setDescription('Sends a message to the channel.')
        .setDefaultMemberPermissions(PermissionFlagsBits.Administrator)
		.addStringOption(option =>
			option.setName('message')
				.setDescription('The message to send')
				.setRequired(true)),
	async execute(interaction) {
		const userInput = interaction.options.getString('message');
        
        if (userInput.length > 2000) {
            return interaction.reply({ content: 'Message is too long (limit is 2000 characters).', flags: MessageFlags.Ephemeral });
        }

		try {
			await interaction.channel.send({ 
                content: userInput, 
                allowedMentions: { parse: [] } 
            });
			await interaction.reply({ content: 'Message sent!', flags: MessageFlags.Ephemeral });
            return `Channel: ${interaction.channel}\nMessage: ${userInput}`;
		} catch (error) {
			console.error(error);
			await interaction.reply({ content: 'Failed to send message: permission missing?', flags: MessageFlags.Ephemeral });
            return null; // Return null if action failed (or handle how you want logging)
		}
	},
};
