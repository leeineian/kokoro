/**
 * AI Command Helpers
 * Shared constants for AI commands and daemons
 */

// Default personality for the AI
const DEFAULT_SYSTEM_PROMPT = "Respond directly and concisely. Do not narrate the conversation, summarize the chat history, or comment on who is speaking unless explicitly asked. Stay in character as a direct participant.";

// Max length for custom system prompts
const MAX_PROMPT_LENGTH = 1000;

module.exports = {
    DEFAULT_SYSTEM_PROMPT,
    MAX_PROMPT_LENGTH
};
