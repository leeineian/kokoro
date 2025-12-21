const { createMockInteraction, createMockCommandOptions, addInteractionTypeMethods } = require('./mockDiscord');

/**
 * Builder for creating chat input command interactions
 */
class ChatInputCommandBuilder {
    constructor(commandName) {
        this.commandName = commandName;
        this.optionsData = [];
        this.interactionOptions = {};
    }

    addStringOption(name, value) {
        this.optionsData.push({ name, value, type: 3 });
        return this;
    }

    addIntegerOption(name, value) {
        this.optionsData.push({ name, value, type: 4 });
        return this;
    }

    addBooleanOption(name, value) {
        this.optionsData.push({ name, value, type: 5 });
        return this;
    }

    addChannelOption(name, value) {
        this.optionsData.push({ name, value, type: 7 });
        return this;
    }

    setSubcommand(name, options = []) {
        this.optionsData.push({ name, type: 1, options });
        return this;
    }

    setSubcommandGroup(name, subcommands = []) {
        this.optionsData.push({ name, type: 2, options: subcommands });
        return this;
    }

    setUser(user) {
        this.interactionOptions.user = user;
        return this;
    }

    setGuild(guild) {
        this.interactionOptions.guild = guild;
        this.interactionOptions.guildId = guild.id;
        return this;
    }

    setChannel(channel) {
        this.interactionOptions.channel = channel;
        this.interactionOptions.channelId = channel.id;
        return this;
    }

    build() {
        const interaction = createMockInteraction({
            type: 2, // ApplicationCommand
            commandName: this.commandName,
            options: createMockCommandOptions(this.optionsData),
            ...this.interactionOptions
        });

        return addInteractionTypeMethods(interaction);
    }
}

/**
 * Builder for creating button interactions
 */
class ButtonInteractionBuilder {
    constructor(customId) {
        this.customId = customId;
        this.interactionOptions = {};
    }

    setUser(user) {
        this.interactionOptions.user = user;
        return this;
    }

    setGuild(guild) {
        this.interactionOptions.guild = guild;
        this.interactionOptions.guildId = guild.id;
        return this;
    }

    setChannel(channel) {
        this.interactionOptions.channel = channel;
        this.interactionOptions.channelId = channel.id;
        return this;
    }

    build() {
        const interaction = createMockInteraction({
            type: 3, // MessageComponent
            customId: this.customId,
            ...this.interactionOptions
        });

        return addInteractionTypeMethods(interaction);
    }
}

/**
 * Builder for creating select menu interactions
 */
class SelectMenuInteractionBuilder {
    constructor(customId) {
        this.customId = customId;
        this.values = [];
        this.interactionOptions = {};
    }

    setValues(values) {
        this.values = values;
        return this;
    }

    setUser(user) {
        this.interactionOptions.user = user;
        return this;
    }

    setGuild(guild) {
        this.interactionOptions.guild = guild;
        this.interactionOptions.guildId = guild.id;
        return this;
    }

    setChannel(channel) {
        this.interactionOptions.channel = channel;
        this.interactionOptions.channelId = channel.id;
        return this;
    }

    build() {
        const interaction = createMockInteraction({
            type: 3, // MessageComponent
            customId: this.customId,
            values: this.values,
            ...this.interactionOptions
        });

        // Override isStringSelectMenu to return true
        interaction.isStringSelectMenu = () => true;
        interaction.isButton = () => false;

        return addInteractionTypeMethods(interaction);
    }
}

/**
 * Helper functions for common interaction patterns
 */
function buildReminderSetCommand(message, when, sendto = 'here') {
    return new ChatInputCommandBuilder('reminder')
        .setSubcommand('set', [
            { name: 'message', value: message, type: 3 },
            { name: 'when', value: when, type: 3 },
            { name: 'sendto', value: sendto, type: 3 }
        ])
        .build();
}

function buildAiPromptSetCommand(text) {
    return new ChatInputCommandBuilder('ai')
        .setSubcommandGroup('prompt', [
            { name: 'set', type: 1, options: [{ name: 'text', value: text, type: 3 }] }
        ])
        .build();
}

function buildDebugStatsCommand() {
    return new ChatInputCommandBuilder('debug')
        .setSubcommand('stats')
        .build();
}

function buildCatSayCommand(message) {
    return new ChatInputCommandBuilder('cat')
        .setSubcommand('say', [
            { name: 'message', value: message, type: 3 }
        ])
        .build();
}

module.exports = {
    ChatInputCommandBuilder,
    ButtonInteractionBuilder,
    SelectMenuInteractionBuilder,
    buildReminderSetCommand,
    buildAiPromptSetCommand,
    buildDebugStatsCommand,
    buildCatSayCommand
};
