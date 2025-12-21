const chalk = require('chalk');

// Log level configuration
const LOG_LEVELS = {
    silent: 0,
    error: 1,
    warn: 2,
    info: 3,
    debug: 4
};

// Check LOG_LEVEL dynamically at runtime instead of caching it
function getCurrentLogLevel() {
    return LOG_LEVELS[process.env.LOG_LEVEL?.toLowerCase()] ?? LOG_LEVELS.info;
}

/**
 * Standardized Console Logger
 * Provides consistent coloring, timestamps, and formatting for terminal output.
 * Respects LOG_LEVEL environment variable for performance optimization.
 */
class ConsoleLogger {
    static getTimestamp() {
        const now = new Date();
        return now.toLocaleTimeString('en-US', { hour12: false });
    }

    static formatMessage(component, message) {
        return `[${this.getTimestamp()}] [${component}] ${message}`;
    }

    /**
     * Check if a log level should execute based on current LOG_LEVEL setting
     * @param {number} level - The log level to check
     * @returns {boolean}
     */
    static shouldLog(level) {
        return getCurrentLogLevel() >= level;
    }

    /**
     * @param {string} component - The component name (e.g., 'Bot', 'Database')
     * @param {string} message - The message to log
     */
    static info(component, message) {
        if (!this.shouldLog(LOG_LEVELS.info)) return;
        console.log(chalk.blue(this.formatMessage(component, message)));
    }

    /**
     * @param {string} component
     * @param {string} message
     */
    static success(component, message) {
        if (!this.shouldLog(LOG_LEVELS.info)) return;
        console.log(chalk.green(this.formatMessage(component, message)));
    }

    /**
     * @param {string} component
     * @param {string} message
     */
    static warn(component, message) {
        if (!this.shouldLog(LOG_LEVELS.warn)) return;
        console.warn(chalk.yellow(this.formatMessage(component, message)));
    }

    /**
     * @param {string} component
     * @param {string} message
     * @param {Error|any} [error] - Optional error object to print details
     */
    static error(component, message, error = null) {
        if (!this.shouldLog(LOG_LEVELS.error)) return;
        console.error(chalk.red(this.formatMessage(component, message)));
        if (error) {
            console.error(error);
        }
    }

    /**
     * @param {string} component
     * @param {string} message
     */
    static debug(component, message) {
        if (!this.shouldLog(LOG_LEVELS.debug)) return;
        console.log(chalk.dim(this.formatMessage(component, message)));
    }
}

module.exports = ConsoleLogger;
