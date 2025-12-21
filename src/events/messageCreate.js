const { Events } = require('discord.js');
const aiChat = require('../daemons/aiChat');

module.exports = {
    name: Events.MessageCreate,
    async execute(message) {
        await aiChat.handleMessage(message, message.client);
    },
};
