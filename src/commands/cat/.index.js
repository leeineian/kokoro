const { SlashCommandBuilder, MessageFlags } = require('discord.js');
const ConsoleLogger = require('../../utils/log/consoleLogger');
const factHandler = require('./fact');
const imageHandler = require('./image');
const sayHandler = require('./say');

// Shared color choices for ANSI formatting
const COLOR_CHOICES = [
	{ name: 'Gray', value: 'gray' },
	{ name: 'Red', value: 'red' },
	{ name: 'Green', value: 'green' },
	{ name: 'Yellow', value: 'yellow' },
	{ name: 'Blue', value: 'blue' },
	{ name: 'Pink', value: 'pink' },
	{ name: 'Cyan', value: 'cyan' },
	{ name: 'White', value: 'white' }
];

/**
 * Cat Command - Main command router for cat-related subcommands
 * Provides cat images, facts, and ASCII art speech bubbles
 */
module.exports = {
	data: new SlashCommandBuilder()
		.setName('cat')
		.setDescription('Cat images and facts')
		.addSubcommand(subcommand =>
			subcommand
				.setName('image')
				.setDescription('Get a random cat image'))
		.addSubcommand(subcommand =>
			subcommand
				.setName('fact')
				.setDescription('Get a random cat fact'))
		.addSubcommand(subcommand =>
			subcommand
				.setName('say')
				.setDescription('Make a cat say something')
				.addStringOption(option =>
					option.setName('message')
						.setDescription('What should the cat say?')
						.setMaxLength(3000)
						.setRequired(true))
				.addStringOption(option =>
					option.setName('msgcolor')
						.setDescription('Color for the message text')
						.setRequired(false)
						.addChoices(...COLOR_CHOICES))
				.addStringOption(option =>
					option.setName('bubcolor')
						.setDescription('Color for the speech bubble borders')
						.setRequired(false)
						.addChoices(...COLOR_CHOICES))
				.addStringOption(option =>
					option.setName('catcolor')
						.setDescription('Color for the cat')
						.setRequired(false)
						.addChoices(...COLOR_CHOICES))),
	
	/**
	 * Routes the interaction to the appropriate subcommand handler
	 * @param {import('discord.js').ChatInputCommandInteraction} interaction - Discord interaction
	 * @returns {Promise<string|void>} - Returns status message from handler or void on error
	 */
	async execute(interaction) {
		try {
			const subcommand = interaction.options.getSubcommand();
			
			switch (subcommand) {
				case 'fact':
					return factHandler.execute(interaction);
				case 'image':
					return imageHandler.execute(interaction);
				case 'say':
					return sayHandler.execute(interaction);
				default:
					ConsoleLogger.warn('CatCommand', `Unknown subcommand: ${subcommand}`);
					await interaction.reply({
						content: 'Unknown cat command! 😿',
						flags: MessageFlags.Ephemeral
					});
					return;
			}
		} catch (error) {
			ConsoleLogger.error('CatCommand', 'Failed to execute cat command:', error);
			
			// Check if we can still reply
			if (!interaction.replied && !interaction.deferred) {
				await interaction.reply({
					content: 'Something went wrong with the cat command! 😿',
					flags: MessageFlags.Ephemeral
				});
			}
		}
	},
};
