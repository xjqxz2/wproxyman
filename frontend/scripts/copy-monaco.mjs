// Copies the Monaco editor runtime from node_modules into public/ so the app
// loads it locally instead of from a CDN (which can fail/white-screen,
// especially behind restricted networks).
//
// Only the languages this app uses are kept (json/xml/html/css/javascript/
// typescript) to keep the shipped bundle small.
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const src = join(root, 'node_modules', 'monaco-editor', 'min', 'vs');
const dest = join(root, 'public', 'monaco', 'vs');

if (!existsSync(src)) {
  console.error('monaco-editor not installed — run: npm install monaco-editor');
  process.exit(1);
}

// Language folders to KEEP under vs/language/.
const keepLanguages = new Set(['json', 'xml', 'html', 'css', 'javascript', 'typescript', 'typescript_2']);
// Simple languages to keep under vs/basic-languages/.
const keepBasic = new Set([
  'json', 'xml', 'html', 'css', 'javascript', 'typescript',
  'plaintext', 'shell', 'sql', 'markdown', 'yaml', 'ini',
]);

rmSync(dest, { recursive: true, force: true });
mkdirSync(dest, { recursive: true });

function shouldSkip(dir, name) {
  if (dir.endsWith('language')) return !keepLanguages.has(name);
  if (dir.endsWith('basic-languages')) return !keepBasic.has(name);
  return false;
}

function copyTree(from, to) {
  for (const entry of readdirSync(from, { withFileTypes: true })) {
    const srcPath = join(from, entry.name);
    const dstPath = join(to, entry.name);
    if (entry.isDirectory()) {
      if (shouldSkip(from, entry.name)) continue;
      mkdirSync(dstPath, { recursive: true });
      copyTree(srcPath, dstPath);
    } else {
      cpSync(srcPath, dstPath);
    }
  }
}

copyTree(src, dest);

// Report size
let bytes = 0;
function sizeOf(p) {
  for (const e of readdirSync(p, { withFileTypes: true })) {
    const fp = join(p, e.name);
    if (e.isDirectory()) sizeOf(fp);
    else bytes += statSync(fp).size;
  }
}
sizeOf(dest);
console.log(`Monaco runtime copied to public/monaco/vs (${(bytes / 1048576).toFixed(1)} MB)`);
