const { mock } = require('bun:test');

/**
 * Mock Discord.js Client
 * Provides a minimal Client implementation for testing
 */
function createMockClient() {
    return {
        user: {
            tag: 'TestBot#0001',
            id: '123456789',
            username: 'TestBot',
            discriminator: '0001',
            setPresence: mock(() => {})
        },
        commands: new Map(),
        componentHandlers: new Map(),
        guilds: {
            cache: Object.assign(new Map(), {
                first: function() { return this.values().next().value; }
            }),
            fetch: mock(async (id) => createMockGuild({ id }))
        },
        channels: {
            cache: Object.assign(new Map(), {
                first: function() { return this.values().next().value; }
            }),
            fetch: mock(async (id) => createMockChannel({ id }))
        },
        ws: {
            ping: 50 // Mock ping value
        }
    };
}

/**
 * Mock Discord.js Guild
 */
function createMockGuild(options = {}) {
    return {
        id: options.id || '987654321',
        name: options.name || 'Test Guild',
        roles: {
            cache: new Map(),
            fetch: mock(async (id) => createMockRole({ id }))
        },
        channels: {
            cache: new Map(),
            fetch: mock(async (id) => createMockChannel({ id }))
        },
        members: {
            cache: new Map(),
            fetch: mock(async (id) => createMockMember({ id }))
        }
    };
}

/**
 * Mock Discord.js Channel
 */
function createMockChannel(options = {}) {
    const channel = {
        id: options.id || 'channel123',
        name: options.name || 'test-channel',
        type: options.type || 0, // 0 = GuildText
        guild: options.guild || createMockGuild(),
        send: mock(async (content) => createMockMessage({ content })),
        fetchWebhooks: mock(async () => []),
        createWebhook: mock(async (name) => createMockWebhook({ name })),
        permissionsFor: mock(() => ({
            has: mock(() => true)
        }))
    };

    if (options.children) {
        channel.children = {
            cache: new Map(options.children.map(ch => [ch.id, ch]))
        };
    }

    return channel;
}

/**
 * Mock Discord.js User
 */
function createMockUser(options = {}) {
    return {
        id: options.id || 'user123',
        username: options.username || 'TestUser',
        discriminator: options.discriminator || '0001',
        tag: `${options.username || 'TestUser'}#${options.discriminator || '0001'}`,
        bot: options.bot || false,
        displayAvatarURL: mock(() => 'https://example.com/avatar.png')
    };
}

/**
 * Mock Discord.js Member
 */
function createMockMember(options = {}) {
    return {
        id: options.id || 'member123',
        user: options.user || createMockUser(),
        roles: {
            cache: new Map(),
            add: mock(async () => {}),
            remove: mock(async () => {})
        },
        permissions: {
            has: mock(() => true)
        }
    };
}

/**
 * Mock Discord.js Role
 */
function createMockRole(options = {}) {
    return {
        id: options.id || 'role123',
        name: options.name || 'Test Role',
        color: options.color || 0,
        setColor: mock(async (color) => {
            return createMockRole({ ...options, color });
        }),
        edit: mock(async (data) => {
            return createMockRole({ ...options, ...data });
        })
    };
}

/**
 * Mock Discord.js Message
 */
function createMockMessage(options = {}) {
    const message = {
        id: options.id || 'msg123',
        content: options.content || 'Test message',
        author: options.author || createMockUser(),
        channel: options.channel || createMockChannel(),
        guild: options.guild || createMockGuild(),
        createdTimestamp: options.createdTimestamp || Date.now(),
        reply: mock(async (content) => createMockMessage({ content })),
        edit: mock(async (content) => createMockMessage({ ...options, content })),
        delete: mock(async () => {}),
        attachments: new Map(options.attachments || []),
        mentions: {
            users: new Map(),
            has: mock((id) => false)
        },
        client: options.client || createMockClient()
    };

    return message;
}

/**
 * Mock Discord.js Webhook
 */
function createMockWebhook(options = {}) {
    return {
        id: options.id || 'webhook123',
        name: options.name || 'Test Webhook',
        send: mock(async (content) => createMockMessage({ content })),
        delete: mock(async () => {})
    };
}

/**
 * Mock Discord.js Interaction (Base)
 */
function createMockInteraction(options = {}) {
    return {
        id: options.id || 'interaction123',
        type: options.type || 2, // 2 = ApplicationCommand
        user: options.user || createMockUser(),
        member: options.member || createMockMember(),
        guild: options.guild || createMockGuild(),
        guildId: options.guildId || '987654321',
        channel: options.channel || createMockChannel(),
        channelId: options.channelId || 'channel123',
        client: options.client || createMockClient(),
        customId: options.customId || undefined,
        replied: options.replied || false,
        deferred: options.deferred || false,
        commandName: options.commandName || undefined,
        options: options.options || createMockCommandOptions(),
        reply: mock(async (response) => {
            return { flags: response.flags };
        }),
        followUp: mock(async (response) => {
            return { flags: response.flags };
        }),
        deferReply: mock(async () => {}),
        editReply: mock(async (response) => {
            return { flags: response.flags };
        }),
        fetchReply: mock(async () => createMockMessage()),
        update: mock(async (response) => {
            return { flags: response.flags };
        })
    };
}

/**
 * Mock Command Options (for slash commands)
 */
function createMockCommandOptions(data = []) {
    return {
        data,
        getString: mock((name) => {
            const option = data.find(opt => opt.name === name);
            return option?.value;
        }),
        getInteger: mock((name) => {
            const option = data.find(opt => opt.name === name);
            return option?.value;
        }),
        getBoolean: mock((name) => {
            const option = data.find(opt => opt.name === name);
            return option?.value;
        }),
        getChannel: mock((name) => {
            const option = data.find(opt => opt.name === name);
            return option?.value;
        }),
        getSubcommand: mock(() => {
            const subcommand = data.find(opt => opt.type === 1);
            return subcommand?.name;
        }),
        getSubcommandGroup: mock(() => {
            const group = data.find(opt => opt.type === 2);
            return group?.name;
        })
    };
}

/**
 * Check if interaction is a specific type
 */
function addInteractionTypeMethods(interaction) {
    interaction.isChatInputCommand = mock(() => interaction.type === 2);
    interaction.isButton = mock(() => interaction.type === 3 && interaction.customId && !interaction.values);
    interaction.isStringSelectMenu = mock(() => interaction.type === 3 && interaction.customId && Array.isArray(interaction.values));
    interaction.isAutocomplete = mock(() => interaction.type === 4);
    interaction.isCommand = mock(() => interaction.isChatInputCommand());
    return interaction;
}

module.exports = {
    createMockClient,
    createMockGuild,
    createMockChannel,
    createMockUser,
    createMockMember,
    createMockRole,
    createMockMessage,
    createMockWebhook,
    createMockInteraction,
    createMockCommandOptions,
    addInteractionTypeMethods
};
