const fs = require('fs');
const path = require('path');

const ROOT_DIR = path.join(__dirname, '../../../');
const ALLOWED_DIRS = ['src'];
const BLACKLIST = ['.env', 'data.db', '.bot.pid', 'node_modules', '.git', 'package-lock.json', '.DS_Store'];

/**
 * Normalizes and validates a path to ensure it stays within the allowed project structure.
 * Returns the absolute path if valid, null otherwise.
 */
function getSafePath(relativePath) {
    if (!relativePath) return null;

    // Remove any leading slashes or dots to prevent absolute/relative escapes
    const normalized = relativePath.replace(/^(\.\.\/|\/)+/, '');
    const absolutePath = path.resolve(ROOT_DIR, normalized);

    // Security Check: Must be within ROOT_DIR/src
    if (!absolutePath.startsWith(path.join(ROOT_DIR, 'src'))) return null;

    // Blacklist Check
    const filename = path.basename(absolutePath);
    if (BLACKLIST.includes(filename) || BLACKLIST.some(b => absolutePath.includes(`/${b}/`))) return null;

    if (!fs.existsSync(absolutePath)) return null;
    
    return absolutePath;
}

/**
 * Returns a simple string map of the codebase structure (src/ only)
 */
let cachedStructure = null;
let lastScan = 0;
const CACHE_TTL = 300000; // 5 minutes

function getStructure() {
    const now = Date.now();
    
    // Return cached structure if still valid
    if (cachedStructure && (now - lastScan) < CACHE_TTL) {
        return cachedStructure;
    }
    
    // Re-scan
    const srcDir = path.join(ROOT_DIR, 'src');
    const structure = [];

    function crawl(dir) {
        const files = fs.readdirSync(dir);
        for (const file of files) {
            const fullPath = path.join(dir, file);
            const relative = path.relative(ROOT_DIR, fullPath);

            if (BLACKLIST.includes(file)) continue;

            if (fs.statSync(fullPath).isDirectory()) {
                crawl(fullPath);
            } else {
                structure.push(relative);
            }
        }
    }

    try {
        crawl(srcDir);
        cachedStructure = structure;
        lastScan = now;
        return structure;
    } catch (e) {
        return cachedStructure || []; // Return cached on error if available
    }
}

/**
 * Force refresh of cached structure (useful if you know files changed)
 */
function refreshStructure() {
    cachedStructure = null;
    return getStructure();
}

/**
 * Reads a project file and returns its content (truncated if too large)
 */
function readFile(relativePath) {
    const safePath = getSafePath(relativePath);
    if (!safePath) return null;

    try {
        const stats = fs.statSync(safePath);
        if (stats.isDirectory()) return null;

        // Limit to 10KB to prevent context overflow
        const buffer = Buffer.alloc(10000);
        const fd = fs.openSync(safePath, 'r');
        const bytesRead = fs.readSync(fd, buffer, 0, 10000, 0);
        fs.closeSync(fd);

        let content = buffer.toString('utf8', 0, bytesRead);
        if (stats.size > 10000) content += "\n...[File Truncated at 10KB]";
        
        return content;
    } catch (e) {
        return null;
    }
}

module.exports = { getStructure, refreshStructure, readFile };

