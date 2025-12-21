/**
 * Safe Timer Utility
 * Handles the 32-bit signed integer limit for setTimeout (approx 24.8 days)
 */

const MAX_DELAY = 2147483647; // 2^31 - 1

/**
 * Sets a timeout that can handle delays larger than 32-bit integer limit.
 * @param {Function} callback - The function to call when the timer expires
 * @param {number} delay - The delay in milliseconds
 * @param {...any} args - Optional arguments to pass to the callback
 * @returns {{ cancel: Function }} - An object with a cancel method
 */
function setLongTimeout(callback, delay, ...args) {
    let timerId;
    let running = true;

    const run = () => {
        if (!running) return;

        if (delay > MAX_DELAY) {
            const currentDelay = MAX_DELAY;
            delay -= MAX_DELAY;
            timerId = setTimeout(run, currentDelay);
        } else {
            timerId = setTimeout(() => {
                if (running) callback(...args);
            }, delay);
        }
    };

    run();

    return {
        cancel: () => {
            running = false;
            if (timerId) clearTimeout(timerId);
        }
    };
}

module.exports = { setLongTimeout };
