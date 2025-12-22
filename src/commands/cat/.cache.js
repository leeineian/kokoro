/**
 * Cat Command Cache
 * 
 * @module commands/cat/cache
 * @description Simple in-memory cache for cat API responses to reduce external API calls
 * and improve response times. Implements TTL-based expiration and LRU-style eviction.
 * 
 * @example
 * const { cacheFact, getCachedFact, cacheImage, getCachedImage } = require('./.cache');
 * 
 * // Cache a cat fact
 * cacheFact('Cats have 32 muscles in each ear');
 * 
 * // Retrieve cached fact (returns null if not found or expired)
 * const fact = getCachedFact();
 * 
 * // Cache an image URL
 * cacheImage('https://cdn2.thecatapi.com/images/abc123.jpg');
 * 
 * // Retrieve cached image
 * const imageUrl = getCachedImage();
 */

// Cache configuration
const CACHE_CONFIG = {
    FACT_TTL: 300000,      // 5 minutes
    IMAGE_TTL: 180000,     // 3 minutes
    MAX_FACTS: 50,
    MAX_IMAGES: 100
};

// In-memory cache stores
const factCache = [];
const imageCache = [];

/**
 * Add a fact to the cache
 * @param {string} fact - Cat fact text
 */
function cacheFact(fact) {
    if (factCache.length >= CACHE_CONFIG.MAX_FACTS) {
        factCache.shift(); // Remove oldest
    }
    factCache.push({ fact, timestamp: Date.now() });
}

/**
 * Get a random fact from cache if available and fresh
 * @returns {string|null} - Cached fact or null
 */
function getCachedFact() {
    if (factCache.length === 0) return null;
    
    const now = Date.now();
    // Filter out expired facts
    const freshFacts = factCache.filter(item => now - item.timestamp < CACHE_CONFIG.FACT_TTL);
    
    if (freshFacts.length === 0) {
        factCache.length = 0; // Clear stale cache
        return null;
    }
    
    // Return random fact from fresh ones
    const randomIndex = Math.floor(Math.random() * freshFacts.length);
    return freshFacts[randomIndex].fact;
}

/**
 * Add an image URL to the cache
 * @param {string} url - Cat image URL
 */
function cacheImage(url) {
    if (imageCache.length >= CACHE_CONFIG.MAX_IMAGES) {
        imageCache.shift(); // Remove oldest
    }
    imageCache.push({ url, timestamp: Date.now() });
}

/**
 * Get a random image URL from cache if available and fresh
 * @returns {string|null} - Cached image URL or null
 */
function getCachedImage() {
    if (imageCache.length === 0) return null;
    
    const now = Date.now();
    // Filter out expired images
    const freshImages = imageCache.filter(item => now - item.timestamp < CACHE_CONFIG.IMAGE_TTL);
    
    if (freshImages.length === 0) {
        imageCache.length = 0; // Clear stale cache
        return null;
    }
    
    // Return random image from fresh ones
    const randomIndex = Math.floor(Math.random() * freshImages.length);
    return freshImages[randomIndex].url;
}

/**
 * Warm up the cache by pre-fetching items
 * @param {Function} fetchFn - Async function to fetch items
 * @param {Function} cacheFn - Function to cache items
 * @param {number} count - Number of items to pre-fetch
 */
async function warmUpCache(fetchFn, cacheFn, count) {
    for (let i = 0; i < count; i++) {
        try {
            const item = await fetchFn();
            if (item) cacheFn(item);
        } catch (e) {
            // Silently fail during warmup
        }
    }
}

module.exports = {
    CACHE_CONFIG,
    cacheFact,
    getCachedFact,
    cacheImage,
    getCachedImage,
    warmUpCache
};
