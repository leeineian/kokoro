const db = require('../utils/database');
const ConsoleLogger = require('../utils/consoleLogger');
const DEFAULT_SYSTEM_PROMPT = "Answer concisely.";
const statusRotator = require('./statusRotator');

// --- CONFIGURATION ---
const MODEL = "default"; // LLM7 default model
const PROCESSED_CACHE_TTL = 60000;

// --- DEDUPLICATION CACHE ---
const processedMessages = new Set();
setInterval(() => processedMessages.clear(), PROCESSED_CACHE_TTL);

// --- DYNAMIC HELPERS ---
const getDynamicContextLimit = () => Math.floor(Math.random() * 8) + 5; // Context size varies between 5 and 12

/**
 * --- MAIN AI HANDLER ---
 */
async function handleMessage(message, client) {
    if (processedMessages.has(message.id)) return;
    processedMessages.add(message.id);

    // 1. Basic Checks
    if (message.author.bot) return;

    const isMentioned = message.mentions.users.has(client.user.id);
    let isReplyToMe = false;

    if (message.reference) {
        try {
            const refMessage = await message.fetchReference();
            isReplyToMe = refMessage.author.id === client.user.id;
        } catch (e) { isReplyToMe = false; }
    }

    if (!isMentioned && !isReplyToMe) return;

    // 2. Fire Typing Indicator & Update Status (Thinking)
    message.channel.sendTyping().catch(() => {});
    client.user.setPresence({ status: 'online' });

    try {
        // 3. Prepare Context
        const dynamicLimit = getDynamicContextLimit();
        const barrierTimestamp = db.aiMemory.getBarrier(message.channel.id);
        
        const [contextMessages] = await Promise.all([
            message.channel.messages.fetch({ limit: dynamicLimit, before: message.id }).catch(() => new Map())
        ]);

        const conversation = [];
        contextMessages.forEach(msg => {
            // Memory Barrier Check (Ignore messages older than the reset timestamp)
            if (msg.createdTimestamp < barrierTimestamp) return;

            const role = msg.author.id === client.user.id ? 'assistant' : 'user';
            const content = msg.cleanContent.replace(/@\S+/g, '').trim();
            if (content) conversation.push({ role, content });
        });
        conversation.reverse();

        let userPrompt = message.cleanContent.replace(new RegExp(`@${client.user.username}`, 'g'), '').trim();
        if (!userPrompt) userPrompt = "*stares silently*";

        // Get Custom System Prompt
        const customSystemPrompt = db.aiPrompts.get(message.author.id) || DEFAULT_SYSTEM_PROMPT;

        // Build Payload
        const messages = [
            { role: "system", content: customSystemPrompt },
            ...conversation,
            { role: "user", content: userPrompt }
        ];

        // 4. Call API (LLM7.io)
        const response = await fetch("https://api.llm7.io/v1/chat/completions", {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "Authorization": `Bearer ${process.env.LLM7_KEY || ""}`
            },
            body: JSON.stringify({
                messages: messages,
                model: MODEL
            })
        });

        if (!response.ok) {
            const errText = await response.text();
            throw new Error(`API ${response.status}: ${errText.substring(0, 100)}`);
        }

        const data = await response.json();
        const replyText = data.choices[0].message.content;

        // 5. Send Reply
        await message.reply({ 
            content: replyText,
            allowedMentions: { repliedUser: false }
        });

        // Revert to Dynamic Status Logic (checks idle time etc.)
        try {
            statusRotator.recordActivity(client); // Reset idle timer
            statusRotator.updateStatus(client);   // Apply status (Online)
        } catch (e) {
            // Fallback if rotator fails or isn't loaded
            client.user.setPresence({ status: 'dnd' });
        }

    } catch (error) {
        ConsoleLogger.error('AI Chat', 'Error:', error);
        await message.reply(`*fizz*... (Error: ${error.message})`).catch(e => ConsoleLogger.error('AI Chat', 'Failed to send error message:', e));
    }
}

module.exports = { handleMessage };
