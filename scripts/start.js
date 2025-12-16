const { execSync, spawn } = require('child_process');

console.log('Checking for zombie processes...');

try {
    // Attempt to kill existing 'node index.js' processes
    // using -f to match the full command line
    execSync('pkill -f "node index.js"');
    console.log('Zombie process killed.');
} catch (error) {
    // pkill returns non-zero if no processes matched, which is fine
}

console.log('Starting Minder...');

// Spawn the main bot process
const child = spawn('node', ['index.js'], { 
    stdio: 'inherit', // Pipe output to this terminal
    cwd: process.cwd(),
    env: process.env
});

child.on('close', (code) => {
    console.log(`Bot exited with code ${code}`);
    process.exit(code);
});
