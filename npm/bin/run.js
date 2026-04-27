#!/usr/bin/env node

const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');

const binDir = path.join(__dirname);
const binaryName = process.platform === 'win32' ? 'chronicle.exe' : 'chronicle';
const binaryPath = path.join(binDir, binaryName);

if (!fs.existsSync(binaryPath)) {
  console.error('Chronicle binary not found. Run: npm rebuild chronicle-mcp');
  console.error('Or install manually: https://github.com/alexdx2/chronicle/releases');
  process.exit(1);
}

// Pass all arguments through to the Go binary
const args = process.argv.slice(2);
const child = spawn(binaryPath, args, {
  stdio: 'inherit',
  env: process.env,
});

child.on('error', (err) => {
  console.error(`Failed to start Chronicle: ${err.message}`);
  process.exit(1);
});

child.on('exit', (code) => {
  process.exit(code || 0);
});
