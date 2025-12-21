const { describe, test, expect } = require('bun:test');

// ============================================================================
// CAT COMMAND
// ============================================================================
const catCommand = require('../../src/commands/cat/index');
const sayHandler = require('../../src/commands/cat/say');

describe('Cat Command', () => {
    test('should have valid slash command data', () => {
        expect(catCommand.data).toBeDefined();
        expect(catCommand.data.name).toBe('cat');
        expect(catCommand.data.description).toBeDefined();
    });

    test('should have subcommands: image, fact, say', () => {
        const options = catCommand.data.options;
        expect(options).toBeDefined();
        
        const subcommands = options.map(opt => opt.name);
        expect(subcommands).toContain('image');
        expect(subcommands).toContain('fact');
        expect(subcommands).toContain('say');
    });

    test('should have execute function', () => {
        expect(typeof catCommand.execute).toBe('function');
    });

    describe('Say Handler', () => {
        test('getVisualWidth should calculate ASCII correctly', () => {
            expect(sayHandler.getVisualWidth('hello')).toBe(5);
            expect(sayHandler.getVisualWidth('cat')).toBe(3);
        });

        test('getVisualWidth should strip ANSI codes', () => {
            const textWithAnsi = '\x1b[31mred\x1b[0m';
            expect(sayHandler.getVisualWidth(textWithAnsi)).toBe(3);
        });

        test('getVisualWidth should handle emoji as width 2', () => {
            expect(sayHandler.getVisualWidth('😀')).toBe(2);
            expect(sayHandler.getVisualWidth('🐱')).toBe(2);
        });

        test('wrapText should wrap at maxWidth', () => {
            const lines = sayHandler.wrapText('hello world this is a test', 10);
            expect(Array.isArray(lines)).toBe(true);
            expect(lines.length).toBeGreaterThan(1);
            
            lines.forEach(line => {
                expect(sayHandler.getVisualWidth(line)).toBeLessThanOrEqual(10);
            });
        });

        test('wrapText should handle empty string', () => {
            const lines = sayHandler.wrapText('', 10);
            expect(lines).toEqual(['']);
        });
    });
});

// ============================================================================
// REMINDER COMMAND
// ============================================================================
const reminderCommand = require('../../src/commands/reminder/index');

describe('Reminder Command', () => {
    test('should have valid slash command data', () => {
        expect(reminderCommand.data).toBeDefined();
        expect(reminderCommand.data.name).toBe('reminder');
        expect(reminderCommand.data.description).toBeDefined();
    });

    test('should have subcommands: set, list', () => {
        const options = reminderCommand.data.options;
        expect(options).toBeDefined();
        
        const subcommands = options.map(opt => opt.name);
        expect(subcommands).toContain('set');
        expect(subcommands).toContain('list');
    });

    test('should have execute function', () => {
        expect(typeof reminderCommand.execute).toBe('function');
    });

    test('should have handlers for interactions', () => {
        expect(reminderCommand.handlers).toBeDefined();
        expect(typeof reminderCommand.handlers).toBe('object');
        
        // Should have dismiss_message handler
        expect(reminderCommand.handlers.dismiss_message).toBeDefined();
        expect(typeof reminderCommand.handlers.dismiss_message).toBe('function');
    });

    test('set subcommand should have required options', () => {
        const setCommand = reminderCommand.data.options.find(opt => opt.name === 'set');
        expect(setCommand).toBeDefined();
        
        const options = setCommand.options;
        const optionNames = options.map(opt => opt.name);
        
        expect(optionNames).toContain('message');
        expect(optionNames).toContain('when');
        expect(optionNames).toContain('sendto');
    });
});

// ============================================================================
// AI COMMAND
// ============================================================================
const aiCommand = require('../../src/commands/ai/index');

describe('AI Command', () => {
    test('should have valid slash command data', () => {
        expect(aiCommand.data).toBeDefined();
        expect(aiCommand.data.name).toBe('ai');
        expect(aiCommand.data.description).toBeDefined();
    });

    test('should have subcommand groups: prompt, memory', () => {
        const options = aiCommand.data.options;
        expect(options).toBeDefined();
        
        const groups = options.map(opt => opt.name);
        expect(groups).toContain('prompt');
        expect(groups).toContain('memory');
    });

    test('should have execute function', () => {
        expect(typeof aiCommand.execute).toBe('function');
    });

    describe('Prompt Subcommand Group', () => {
        test('should have set, reset, and view subcommands', () => {
            const promptGroup = aiCommand.data.options.find(opt => opt.name === 'prompt');
            expect(promptGroup).toBeDefined();
            
            const subcommands = promptGroup.options.map(opt => opt.name);
            expect(subcommands).toContain('set');
            expect(subcommands).toContain('reset');
            expect(subcommands).toContain('view');
        });

        test('set subcommand should require text option', () => {
            const promptGroup = aiCommand.data.options.find(opt => opt.name === 'prompt');
            const setCommand = promptGroup.options.find(opt => opt.name === 'set');
            expect(setCommand).toBeDefined();
            
            const textOption = setCommand.options.find(opt => opt.name === 'text');
            expect(textOption).toBeDefined();
            expect(textOption.required).toBe(true);
        });
    });

    describe('Memory Subcommand Group', () => {
        test('should have reset subcommand', () => {
            const memoryGroup = aiCommand.data.options.find(opt => opt.name === 'memory');
            expect(memoryGroup).toBeDefined();
            
            const resetCommand = memoryGroup.options.find(opt => opt.name === 'reset');
            expect(resetCommand).toBeDefined();
        });
    });
});

// ============================================================================
// DEBUG COMMAND
// ============================================================================
const debugCommand = require('../../src/commands/debug/index');

describe('Debug Command', () => {
    test('should have valid slash command data', () => {
        expect(debugCommand.data).toBeDefined();
        expect(debugCommand.data.name).toBe('debug');
        expect(debugCommand.data.description).toBeDefined();
    });

    test('should have execute function', () => {
        expect(typeof debugCommand.execute).toBe('function');
    });

    test('should have handlers object', () => {
        expect(debugCommand.handlers).toBeDefined();
        expect(typeof debugCommand.handlers).toBe('object');
    });

    test('should have DM permission disabled', () => {
        expect(debugCommand.data.dm_permission).toBe(false);
    });

    test('should require administrator permissions', () => {
        expect(debugCommand.data.default_member_permissions).toBeDefined();
    });

    describe('Subcommands', () => {
        test('should have stats subcommand', () => {
            const options = debugCommand.data.options;
            const stats = options.find(opt => opt.name === 'stats');
            expect(stats).toBeDefined();
        });

        test('should have ping subcommand', () => {
            const options = debugCommand.data.options;
            const ping = options.find(opt => opt.name === 'ping');
            expect(ping).toBeDefined();
        });

        test('should have log subcommand', () => {
            const options = debugCommand.data.options;
            const log = options.find(opt => opt.name === 'log');
            expect(log).toBeDefined();
        });
    });

    describe('Subcommand Groups', () => {
        test('should have rolecolor group', () => {
            const options = debugCommand.data.options;
            const rolecolor = options.find(opt => opt.name === 'rolecolor');
            expect(rolecolor).toBeDefined();
        });

        test('should have message group', () => {
            const options = debugCommand.data.options;
            const message = options.find(opt => opt.name === 'message');
            expect(message).toBeDefined();
        });

        test('should have webhook-looper group', () => {
            const options = debugCommand.data.options;
            const webhookLooper = options.find(opt => opt.name === 'webhook-looper');
            expect(webhookLooper).toBeDefined();
        });
    });

    describe('Webhook Looper Subcommands', () => {
        test('should have list, set, start, stop, purge subcommands', () => {
            const options = debugCommand.data.options;
            const webhookLooper = options.find(opt => opt.name === 'webhook-looper');
            expect(webhookLooper).toBeDefined();
            
            const subcommands = webhookLooper.options.map(opt => opt.name);
            expect(subcommands).toContain('list');
            expect(subcommands).toContain('set');
            expect(subcommands).toContain('start');
            expect(subcommands).toContain('stop');
            expect(subcommands).toContain('purge');
        });
    });
});
