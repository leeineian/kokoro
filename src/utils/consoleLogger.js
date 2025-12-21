const chalk = require('chalk');

/**
 * Standardized Console Logger
 * Provides consistent coloring, timestamps, and formatting for terminal output.
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
     * @param {string} component - The component name (e.g., 'Bot', 'Database')
     * @param {string} message - The message to log
     */
    static info(component, message) {
        console.log(chalk.blue(this.formatMessage(component, message)));
    }

    /**
     * @param {string} component
     * @param {string} message
     */
    static success(component, message) {
        console.log(chalk.green(this.formatMessage(component, message)));
    }

    /**
     * @param {string} component
     * @param {string} message
     */
    static warn(component, message) {
        console.warn(chalk.yellow(this.formatMessage(component, message)));
    }

    /**
     * @param {string} component
     * @param {string} message
     * @param {Error|any} [error] - Optional error object to print details
     */
    static error(component, message, error = null) {
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
        // Only log debug if needed, or use dim color
        console.log(chalk.dim(this.formatMessage(component, message)));
    }
}

module.exports = ConsoleLogger;
