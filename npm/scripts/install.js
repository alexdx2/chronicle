#!/usr/bin/env node

const { execSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const https = require('https');
const http = require('http');

const VERSION = require('../package.json').version;
const REPO = 'https://gitlab.com/Alex_dx3/depbot';

const PLATFORM_MAP = {
  'darwin-x64': 'oracle-darwin-amd64',
  'darwin-arm64': 'oracle-darwin-arm64',
  'linux-x64': 'oracle-linux-amd64',
  'linux-arm64': 'oracle-linux-arm64',
  'win32-x64': 'oracle-windows-amd64.exe',
};

const platform = `${process.platform}-${process.arch}`;
const binaryName = PLATFORM_MAP[platform];

if (!binaryName) {
  console.error(`Unsupported platform: ${platform}`);
  console.error(`Supported: ${Object.keys(PLATFORM_MAP).join(', ')}`);
  console.error('You can build from source: go build -o oracle ./cmd/oracle');
  process.exit(1);
}

const binDir = path.join(__dirname, '..', 'bin');
const binaryPath = path.join(binDir, process.platform === 'win32' ? 'oracle.exe' : 'oracle');

// Try downloading from GitLab releases
const releaseURL = `${REPO}/-/releases/v${VERSION}/downloads/${binaryName}`;

console.log(`Oracle MCP v${VERSION}`);
console.log(`Platform: ${platform} → ${binaryName}`);
console.log(`Downloading from: ${releaseURL}`);

function download(url, dest, redirects = 0) {
  if (redirects > 5) {
    console.error('Too many redirects');
    fallbackToBuild();
    return;
  }

  const client = url.startsWith('https') ? https : http;
  client.get(url, { headers: { 'User-Agent': 'oracle-mcp-installer' } }, (res) => {
    if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
      download(res.headers.location, dest, redirects + 1);
      return;
    }

    if (res.statusCode !== 200) {
      console.log(`Download failed (${res.statusCode}). Building from source...`);
      fallbackToBuild();
      return;
    }

    const file = fs.createWriteStream(dest);
    res.pipe(file);
    file.on('finish', () => {
      file.close();
      if (process.platform !== 'win32') {
        fs.chmodSync(dest, 0o755);
      }
      console.log(`✓ Oracle installed at ${dest}`);
    });
  }).on('error', (err) => {
    console.log(`Download error: ${err.message}. Building from source...`);
    fallbackToBuild();
  });
}

function fallbackToBuild() {
  try {
    // Check if Go is available
    execSync('go version', { stdio: 'pipe' });
    console.log('Building Oracle from source...');

    const srcDir = path.join(__dirname, '..');
    const goSrcDir = path.join(srcDir, '.go-src');

    // Clone if needed
    if (!fs.existsSync(goSrcDir)) {
      console.log('Cloning repository...');
      execSync(`git clone --depth 1 --branch v${VERSION} ${REPO}.git ${goSrcDir}`, { stdio: 'inherit' });
    }

    // Build
    console.log('Compiling...');
    execSync(`go build -ldflags "-s -w" -o ${binaryPath} ./cmd/oracle`, { cwd: goSrcDir, stdio: 'inherit' });

    if (process.platform !== 'win32') {
      fs.chmodSync(binaryPath, 0o755);
    }
    console.log(`✓ Oracle built at ${binaryPath}`);
  } catch (e) {
    console.error('');
    console.error('Could not download or build Oracle.');
    console.error('');
    console.error('Manual install:');
    console.error(`  1. Download from: ${REPO}/-/releases`);
    console.error('  2. Or build from source: git clone ... && go build -o oracle ./cmd/oracle');
    console.error('');
    // Don't fail the install — the run.js wrapper will show an error if binary is missing
  }
}

// Ensure bin dir exists
fs.mkdirSync(binDir, { recursive: true });

download(releaseURL, binaryPath);
