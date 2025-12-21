const db = require('../utils/database');
const ConsoleLogger = require('../utils/consoleLogger');
const { DEFAULT_SYSTEM_PROMPT, MODEL, PROCESSED_CACHE_TTL } = require('../configs/ai');
const statusRotator = require('./statusRotator');
const codebase = require('../utils/codebase');
const { tavily } = require("@tavily/core");

// --- CONFIGURATION ---
const LLM7_ENDPOINT = "https://api.llm7.io/v1/chat/completions";
const tvly = tavily({ apiKey: process.env.TAVILY_API_KEY || "" });

// --- SEARCH CONFIG ---
const SEARCH_KEYWORDS = ['who', 'what', 'where', 'when', 'how', 'why', 'price', 'news', 'latest', 'current', 'today', 'bitcoin', 'crypto', 'stock', 'weather'];

// --- SEARCH HELPER ---
async function getTavilySearch(query) {
    try {
        const result = await tvly.search(query, {
            searchDepth: "basic",
            maxResults: 3
        });
        
        if (!result || !result.results || result.results.length === 0) return null;
        
        return result.results.map(r => 
            `- ${r.title}\n  URL: ${r.url}\n  Context: ${r.content}`
        ).join('\n\n');
    } catch (e) {
        ConsoleLogger.warn('AI Chat', 'Tavily search failed:', e.message);
        return null;
    }
}

// --- DEDUPLICATION CACHE ---
const processedMessages = new Set();
setInterval(() => processedMessages.clear(), PROCESSED_CACHE_TTL);

/**
 * --- MAIN AI HANDLER ---
 */
async function handleMessage(message, client) {
    if (processedMessages.has(message.id)) return;
    processedMessages.add(message.id);

    // 1. Basic Checks
    if (message.author.id === client.user.id) return;
    if (message.system) return;

    const isMentioned = message.mentions.users.has(client.user.id);
    let isReplyToMe = false;

    if (!isMentioned && message.reference) {
        try {
            const refMessage = await message.fetchReference();
            isReplyToMe = refMessage.author.id === client.user.id;
        } catch (e) { isReplyToMe = false; }
    }

    if (!isMentioned && !isReplyToMe) return;

    ConsoleLogger.info('AI Chat', `Processing message from ${message.author.tag} in #${message.channel.name}`);

    // 2. Fire Typing Indicator & Update Status (Thinking)
    const sendTypingInterval = setInterval(() => {
        message.channel.sendTyping().catch(() => {});
    }, 8000);
    message.channel.sendTyping().catch(() => {});
    
    client.user.setPresence({ status: 'online' });

    try {
        // 3. Prepare Context
        ConsoleLogger.info('AI Chat', 'Building history...');
        const barrierTimestamp = db.aiMemory.getBarrier(message.channel.id);
        const contextMessages = await message.channel.messages.fetch({ limit: 50, before: message.id }).catch(() => new Map());

        const history = [];
        const rawContext = Array.from(contextMessages.values())
            .filter(msg => msg.createdTimestamp >= barrierTimestamp)
            .reverse(); // Now oldest to newest

        for (const msg of rawContext) {
            const isFromMe = msg.author.id === client.user.id;
            const mentionsMe = msg.mentions.users.has(client.user.id);
            const isReplyToMe = msg.mentions.repliedUser?.id === client.user.id;

            if (!isFromMe && !mentionsMe && !isReplyToMe) continue;
            if (msg.content && msg.content.includes('🧠 **AI Memory Wiped.**')) continue;

            const role = isFromMe ? 'assistant' : 'user';
            const cleanContent = msg.cleanContent.replace(/@\S+/g, '').trim();
            const name = message.guild?.members.cache.get(msg.author.id)?.displayName || msg.author.username;
            
            if (cleanContent) {
                const text = isFromMe ? cleanContent : `${name}: ${cleanContent}`;
                history.push({ role, content: text });
            }
        }

        let userPromptText = message.cleanContent.replace(new RegExp(`@${client.user.username}`, 'g'), '').trim();
        const currentName = message.member ? message.member.displayName : message.author.username;

        // Attachment Handling
        if (message.attachments.size > 0) {
            const attachmentPrompts = [];
            for (const [id, attachment] of message.attachments) {
                const isText = attachment.contentType?.startsWith('text/') || 
                               ['.js', '.py', '.txt', '.md', '.json', '.html', '.css', '.c', '.cpp', '.java', '.go', '.rs', '.log', '.sh'].some(ext => attachment.name.endsWith(ext));
                
                if (isText && attachment.size < 50000) {
                    try {
                        const response = await fetch(attachment.url);
                        if (response.ok) {
                            const text = await response.text();
                            const truncatedText = text.length > 20000 ? text.substring(0, 20000) + "\n...[Truncated]" : text;
                            attachmentPrompts.push(`\n[File Attachment: ${attachment.name}]\n\`\`\`\n${truncatedText}\n\`\`\``);
                        }
                    } catch (e) {
                        ConsoleLogger.error('AI Chat', `Failed to read attachment ${attachment.name}:`, e);
                    }
                }
            }
            if (attachmentPrompts.length > 0) userPromptText += "\n" + attachmentPrompts.join("\n");
        }

        if (!userPromptText.trim()) userPromptText = "*stares silently*";

        // --- WEB SEARCH (TAVILY) ---
        let webContext = "";
        const lowerPrompt = userPromptText.toLowerCase();
        const hasKeyword = SEARCH_KEYWORDS.some(k => lowerPrompt.includes(k));
        const isQuestion = userPromptText.includes('?');

        // Only search if it's a question containing a keyword, OR a very long prompt with explicit keywords
        if ((hasKeyword && (isQuestion || userPromptText.length > 40)) && !userPromptText.includes('*')) {
            ConsoleLogger.info('AI Chat', 'Triggering Tavily search...');
            const results = await getTavilySearch(userPromptText);
            if (results) {
                ConsoleLogger.info('AI Chat', 'Search results found.');
                webContext = `\n\n[Real-Time Information (Found via Web Search)]:\n${results}`;
            } else {
                ConsoleLogger.info('AI Chat', 'Search returned no results.');
            }
        }

        // --- CODEBASE CONTEXT ---
        const projectStructure = codebase.getStructure();
        let codeContext = "";
        const words = userPromptText.split(/\s+/);
        const mentionedFiles = new Set();
        
        for (const word of words) {
            const cleanWord = word.replace(/[().,?!]/g, '');
            if (cleanWord.includes('.') && projectStructure.some(p => p.includes(cleanWord))) {
                const actualPath = projectStructure.find(p => p.endsWith(cleanWord));
                if (actualPath && !mentionedFiles.has(actualPath)) {
                    const content = codebase.readFile(actualPath);
                    if (content) {
                        mentionedFiles.add(actualPath);
                        codeContext += `\n\n[Project File: ${actualPath}]\n\`\`\`javascript\n${content}\n\`\`\``;
                    }
                }
            }
        }

        // --- TIME CONTEXT ---
        const now = new Date();
        const timeString = now.toLocaleString('en-US', { 
            timeZone: 'Asia/Manila', 
            weekday: 'long', year: 'numeric', month: 'long', day: 'numeric', 
            hour: '2-digit', minute: '2-digit', hour12: true 
        });

        const customSystemPrompt = db.aiPrompts.get(message.author.id) || DEFAULT_SYSTEM_PROMPT;
        const systemPrompt = `${customSystemPrompt}\n\n[Context]\nTime: ${timeString} (UTC+8)\nUser: ${currentName}\nProject Structure: ${projectStructure.join(', ')}${webContext}${codeContext}`;

        // 4. Call LLM7 API
        ConsoleLogger.info('AI Chat', `Calling LLM7 (${MODEL})...`);
        const response = await fetch(LLM7_ENDPOINT, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${process.env.LLM7_KEY}`
            },
            body: JSON.stringify({
                model: MODEL,
                messages: [
                    { role: 'system', content: systemPrompt },
                    ...history,
                    { role: 'user', content: userPromptText }
                ]
            })
        });

        if (!response.ok) {
            const errorData = await response.text();
            throw new Error(`LLM7 API Error: ${response.status} - ${errorData}`);
        }

        const data = await response.json();
        let replyText = data.choices[0].message.content.trim();

        // 5. Post-Processing (Stripping prefixes if hallucinated)
        const botNames = [client.user.username, 'AI', 'Assistant', 'Minder'];
        if (message.guild) {
            const botMember = message.guild.members.me || await message.guild.members.fetchMe().catch(() => null);
            if (botMember?.displayName) botNames.push(botMember.displayName);
        }

        const escapeRegex = (str) => str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        const namePattern = [...new Set(botNames)].filter(Boolean).map(escapeRegex).join('|');
        const nameRegex = new RegExp(`^(\\[BOT\\]\\s*)+|^(${namePattern})\\s*[:,-]\\s*`, 'i');

        while (nameRegex.test(replyText)) {
            replyText = replyText.replace(nameRegex, '').trim();
        }

        // 6. Send Reply
        const chunks = splitMessage(replyText);
        for (const chunk of chunks) {
            await message.reply({ 
                content: chunk,
                allowedMentions: { repliedUser: false }
            });
        }

    } catch (error) {
        ConsoleLogger.error('AI Chat', 'Hybrid AI Error:', error);
        await message.reply(`*fizz*... (Error: ${error.message})`).catch(() => {});
    } finally {
        clearInterval(sendTypingInterval);
        try {
            statusRotator.recordActivity(client);
            statusRotator.updateStatus(client);
        } catch (e) {
            client.user.setPresence({ status: 'dnd' });
        }
    }
}

function splitMessage(text, maxLength = 2000) {
    if (text.length <= maxLength) return [text];
    const chunks = [];
    let currentChunk = '';
    const lines = text.split('\n');
    for (const line of lines) {
        if ((currentChunk + line + '\n').length > maxLength) {
            chunks.push(currentChunk);
            currentChunk = line + '\n';
        } else {
            currentChunk += line + '\n';
        }
    }
    if (currentChunk) chunks.push(currentChunk);
    return chunks;
}

module.exports = { handleMessage };

