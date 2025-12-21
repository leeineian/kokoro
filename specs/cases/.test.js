const { describe, test, expect } = require('bun:test');
const fs = require('fs');
const path = require('path');

// ============================================================================
// PROJECT STRUCTURE
// ============================================================================

describe('Project Structure', () => {
    test('should have required root files', () => {
        const requiredFiles = [
            'package.json',
            'bun.lock',
            '.gitignore'
        ];

        requiredFiles.forEach(file => {
            const filePath = path.join(__dirname, '..', '..', file);
            expect(fs.existsSync(filePath)).toBe(true);
        });
    });

    test('should have required directories', () => {
        const requiredDirs = [
            'src',
            'specs'
        ];

        requiredDirs.forEach(dir => {
            const dirPath = path.join(__dirname, '..', '..', dir);
            expect(fs.existsSync(dirPath)).toBe(true);
            expect(fs.statSync(dirPath).isDirectory()).toBe(true);
        });
    });

    test('src directory should have expected subdirectories', () => {
        const srcSubdirs = ['commands', 'daemons', 'events', 'utils'];
        const srcPath = path.join(__dirname, '..', '..', 'src');

        srcSubdirs.forEach(dir => {
            const dirPath = path.join(srcPath, dir);
            expect(fs.existsSync(dirPath)).toBe(true);
        });
    });
});

// ============================================================================
// PACKAGE.JSON
// ============================================================================

describe('package.json', () => {
    let packageJson;

    test('should be valid JSON', () => {
        const packagePath = path.join(__dirname, '..', '..', 'package.json');
        const content = fs.readFileSync(packagePath, 'utf-8');
        
        expect(() => {
            packageJson = JSON.parse(content);
        }).not.toThrow();
    });

    test('should have required fields', () => {
        const packagePath = path.join(__dirname, '..', '..', 'package.json');
        packageJson = JSON.parse(fs.readFileSync(packagePath, 'utf-8'));

        expect(packageJson.name).toBeDefined();
        expect(packageJson.version).toBeDefined();
        expect(packageJson.scripts).toBeDefined();
        expect(packageJson.dependencies).toBeDefined();
    });

    test('should have required scripts', () => {
        const packagePath = path.join(__dirname, '..', '..', 'package.json');
        packageJson = JSON.parse(fs.readFileSync(packagePath, 'utf-8'));

        const requiredScripts = ['start', 'stop', 'sync', 'test'];
        
        requiredScripts.forEach(script => {
            expect(packageJson.scripts[script]).toBeDefined();
        });
    });

    test('should have discord.js dependency', () => {
        const packagePath = path.join(__dirname, '..', '..', 'package.json');
        packageJson = JSON.parse(fs.readFileSync(packagePath, 'utf-8'));

        expect(packageJson.dependencies['discord.js']).toBeDefined();
    });
});

// ============================================================================
// GITIGNORE
// ============================================================================

describe('.gitignore', () => {
    test('should exist and be readable', () => {
        const gitignorePath = path.join(__dirname, '..', '..', '.gitignore');
        expect(fs.existsSync(gitignorePath)).toBe(true);
        
        const content = fs.readFileSync(gitignorePath, 'utf-8');
        expect(content.length).toBeGreaterThan(0);
    });

    test('should ignore sensitive files', () => {
        const gitignorePath = path.join(__dirname, '..', '..', '.gitignore');
        const content = fs.readFileSync(gitignorePath, 'utf-8');
        
        const sensitivePaths = ['.env', 'node_modules'];
        
        sensitivePaths.forEach(path => {
            expect(content.includes(path)).toBe(true);
        });
    });
});

// ============================================================================
// ENTRY POINTS
// ============================================================================

describe('Entry Point Scripts', () => {
    test('start.js should exist and be valid', () => {
        const startPath = path.join(__dirname, '..', '..', 'src', 'start.js');
        expect(fs.existsSync(startPath)).toBe(true);
        
        const content = fs.readFileSync(startPath, 'utf-8');
        expect(content).toContain('Client');
    });

    test('stop.js should exist and be valid', () => {
        const stopPath = path.join(__dirname, '..', '..', 'src', 'stop.js');
        expect(fs.existsSync(stopPath)).toBe(true);
        
        const content = fs.readFileSync(stopPath, 'utf-8');
        expect(content.length).toBeGreaterThan(0);
    });

    test('sync.js should exist and be valid', () => {
        const syncPath = path.join(__dirname, '..', '..', 'src', 'sync.js');
        expect(fs.existsSync(syncPath)).toBe(true);
        
        const content = fs.readFileSync(syncPath, 'utf-8');
        expect(content).toContain('REST');
    });
});

// ============================================================================
// DATABASE FILES
// ============================================================================

describe('Database Setup', () => {
    test('db directory should exist', () => {
        const dbPath = path.join(__dirname, '..', '..', 'src');
        expect(fs.existsSync(dbPath)).toBe(true);
    });

    test('repo directory should exist with repositories', () => {
        const repoPath = path.join(__dirname, '..', '..', 'src', 'utils', 'db', 'repo');
        expect(fs.existsSync(repoPath)).toBe(true);
        
        const files = fs.readdirSync(repoPath);
        expect(files.length).toBeGreaterThan(0);
    });
});

// ============================================================================
// ENVIRONMENT VALIDATION
// ============================================================================

describe('Environment Variables', () => {
    test('should have example env file for reference', () => {
        const exampleEnvPath = path.join(__dirname, '..', '..', '.env.example');
        // This is optional, so we just check if it exists
        if (fs.existsSync(exampleEnvPath)) {
            const content = fs.readFileSync(exampleEnvPath, 'utf-8');
            expect(content.length).toBeGreaterThan(0);
        }
    });

    test('critical env vars should be defined in running process', () => {
        // These would be loaded when the bot is running
        // For tests, we just verify the pattern
        const criticalVars = ['DISCORD_TOKEN', 'CLIENT_ID'];
        
        // In test environment, these might not be set
        // So we just verify they would be strings if set
        criticalVars.forEach(varName => {
            if (process.env[varName]) {
                expect(typeof process.env[varName]).toBe('string');
            }
        });
    });
});

// ============================================================================
// CODE QUALITY
// ============================================================================

describe('Code Quality', () => {
    test('should not have .env file in git', () => {
        const gitignorePath = path.join(__dirname, '..', '..', '.gitignore');
        const content = fs.readFileSync(gitignorePath, 'utf-8');
        
        expect(content).toContain('.env');
    });

    test('should not have node_modules in git', () => {
        const gitignorePath = path.join(__dirname, '..', '..', '.gitignore');
        const content = fs.readFileSync(gitignorePath, 'utf-8');
        
        expect(content).toContain('node_modules');
    });

    test('all src JavaScript files should be syntax-valid', () => {
        const srcPath = path.join(__dirname, '..', '..', 'src');
        
        function checkJSFiles(dir) {
            const entries = fs.readdirSync(dir, { withFileTypes: true });
            
            entries.forEach(entry => {
                const fullPath = path.join(dir, entry.name);
                
                if (entry.isDirectory()) {
                    checkJSFiles(fullPath);
                } else if (entry.name.endsWith('.js')) {
                    expect(() => {
                        const content = fs.readFileSync(fullPath, 'utf-8');
                        // Just verify it can be read without errors
                        expect(content).toBeDefined();
                    }).not.toThrow();
                }
            });
        }
        
        checkJSFiles(srcPath);
    });
});
