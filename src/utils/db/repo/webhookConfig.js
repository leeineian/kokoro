const db = require('../client');

const addRawCategory = (categoryId, categoryName) => {
    return db.run('INSERT OR REPLACE INTO ping_categories (categoryId, categoryName) VALUES (?, ?)', [categoryId, categoryName]);
};

const getAllRawCategories = () => {
    return db.query('SELECT * FROM ping_categories').all();
};

const deleteRawCategory = (categoryId) => {
    return db.run('DELETE FROM ping_categories WHERE categoryId = ?', [categoryId]);
};

const clearAllRawCategories = () => {
    return db.run('DELETE FROM ping_categories');
};

module.exports = {
    addRawCategory,
    getAllRawCategories,
    deleteRawCategory,
    clearAllRawCategories
};
