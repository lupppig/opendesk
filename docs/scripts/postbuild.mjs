// After `astro build`: hash every inline executable script in the output and write
// the CSP into dist/_headers, so the policy allows exactly what this build ships.
import { createHash } from 'node:crypto';
import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const dist = fileURLToPath(new URL('../dist/', import.meta.url));

const files = [];
const walk = (dir) => {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p);
    else if (p.endsWith('.html')) files.push(p);
  }
};
walk(dist);

const hashes = new Set();
const re = /<script(?![^>]*\bsrc=)([^>]*)>([\s\S]*?)<\/script>/gi;
for (const f of files) {
  const html = readFileSync(f, 'utf8');
  for (const m of html.matchAll(re)) {
    if (/type=["']?application\/(ld\+)?json/i.test(m[1])) continue;
    if (!m[2].trim()) continue;
    hashes.add(`'sha256-${createHash('sha256').update(m[2], 'utf8').digest('base64')}'`);
  }
}

const headersPath = join(dist, '_headers');
const headers = readFileSync(headersPath, 'utf8').replaceAll('__SCRIPT_SRC__', [...hashes].join(' '));
writeFileSync(headersPath, headers);
console.log(`[postbuild] CSP written with ${hashes.size} inline script hash(es) across ${files.length} pages`);
if (headers.includes('__')) throw new Error('[postbuild] an unfilled placeholder is left in dist/_headers');
