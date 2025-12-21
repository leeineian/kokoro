const { mock } = require('bun:test');

/**
 * Global test setup - runs before all test files
 * Mocks ConsoleLogger to suppress all output during test runs
 */
const ConsoleLogger = require('../src/utils/log/consoleLogger');

// Save original methods for cleanup (if needed)
global.__originalConsoleLoggerMethods = {
    info: ConsoleLogger.info,
    success: ConsoleLogger.success,
    warn: ConsoleLogger.warn,
    error: ConsoleLogger.error,
    debug: ConsoleLogger.debug
};

// Suppress all log methods in tests
ConsoleLogger.info = mock(() => {});
ConsoleLogger.success = mock(() => {});
ConsoleLogger.warn = mock(() => {});
ConsoleLogger.error = mock(() => {});
ConsoleLogger.debug = mock(() => {});
