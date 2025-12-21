const path = require('path');

module.exports = {
   LIMITS: {
       MAX_TIMEOUT_MS: 2147483647, // 32-bit integer limit
   },
   PATHS: {
       // Root directory is src/configs/../.. = .
       PID_FILE: path.join(__dirname, '../../.bot.pid')
   }
};
