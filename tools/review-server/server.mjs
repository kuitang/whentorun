#!/usr/bin/env node
// Whentorun mockup review server — tailnet-only, zero npm deps (node:http/node:fs).
// Same pattern as ~/git/merrit/tools/review-server. Kui browses this from his
// iPhone over Tailscale. See README.md alongside.
//
// Invariants:
//   - Binds the machine's TAILNET IPv4 ONLY (never 0.0.0.0, never 127.0.0.1).
//     IP is discovered at startup via `tailscale ip -4`; override with HOST env.
//   - web/ is the DOCUMENT ROOT: mockup pages at /mockups/<v>.html reference
//     /static/... URLs, so those paths must resolve exactly as the Go app
//     would serve them.
//   - test-artifacts/ is served under /artifacts/ (screenshots land in
//     test-artifacts/mockups/).
//   - Fresh fs checks per request — no caching, the index always reflects disk.
//   - Path-traversal guard on every file route.
//   - Range requests supported (iOS Safari needs 206 for any video).

import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';

const REPO = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..', '..');
const PORT = Number(process.env.PORT || 8488); // registered in ~/git/stockski/CONTRACTS.md §6
const HOST = process.env.HOST ||
  execFileSync('tailscale', ['ip', '-4']).toString().trim().split('\n')[0];

const WEB_ROOT = path.join(REPO, 'web');            // document root: /mockups/*, /static/*
const ARTIFACTS_ROOT = path.join(REPO, 'test-artifacts'); // served under /artifacts/*

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.json': 'application/json',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.webp': 'image/webp',
  '.svg': 'image/svg+xml',
  '.gif': 'image/gif',
  '.woff2': 'font/woff2',
  '.woff': 'font/woff',
  '.ttf': 'font/ttf',
  '.mp4': 'video/mp4',
  '.webm': 'video/webm',
  '.txt': 'text/plain; charset=utf-8',
  '.md': 'text/plain; charset=utf-8',
};

// ------------------------------------------------------------------- html
const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
const CSS = `
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body { margin: 0 auto; max-width: 720px; padding: 16px;
         font: 17px/1.55 ui-serif, Georgia, 'Times New Roman', serif; }
  h1, h2, h3 { font-family: system-ui, -apple-system, sans-serif; line-height: 1.25; }
  h1 { font-size: 1.5rem; } h2 { font-size: 1.2rem; margin-top: 1.6em; }
  a { color: inherit; }
  a.card { display: block; min-height: 32px; padding: 12px 14px; margin: 6px 0;
    border: 1px solid rgba(127,127,127,.4); border-radius: 10px; text-decoration: none;
    font-family: system-ui, -apple-system, sans-serif; }
  a.card.missing { opacity: .45; pointer-events: none; }
  ul.files { list-style: none; padding: 0; margin: 0; }
  img.thumb { max-width: 300px; width: 100%; border-radius: 8px; display: block;
        margin: 8px 0 20px; border: 1px solid rgba(127,127,127,.3); }
  .empty { opacity: .6; font-style: italic; }
  .note { opacity: .7; font-size: .85em; font-family: system-ui, -apple-system, sans-serif; }
  .stamp { opacity: .55; font-size: .78em; font-weight: normal; display: block; margin-top: 2px; }
  h3 { margin-bottom: .2em; opacity: .8; }
`;
const page = (title, body) => `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(title)}</title><style>${CSS}</style></head>
<body>${body}</body></html>`;

// Fresh glob per request: [{name, mtime}] for non-hidden files with ext,
// newest-mtime first.
function globWithStat(dir, ext) {
  try {
    return fs.readdirSync(dir)
      .filter((f) => f.toLowerCase().endsWith(ext) && !f.startsWith('.'))
      .map((name) => {
        try { return { name, mtime: fs.statSync(path.join(dir, name)).mtimeMs }; }
        catch { return null; }
      })
      .filter(Boolean)
      .sort((a, b) => b.mtime - a.mtime);
  } catch { return []; }
}

const titleCase = (base) => base.split(/[-_]+/).filter(Boolean)
  .map((w) => w[0].toUpperCase() + w.slice(1)).join(' ');
const stamp = (mtime) => new Date(mtime).toLocaleString('en-US', {
  month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
});

function indexPage() {
  // Mockups: newest first, clustered by prefix (first hyphen token), so
  // e.g. ledger-v2-* iterations sit together. Cluster order follows the
  // newest file in each cluster.
  const mockups = globWithStat(path.join(WEB_ROOT, 'mockups'), '.html');
  const groups = new Map(); // prefix -> files (insertion order = newest-first)
  for (const f of mockups) {
    const prefix = f.name.split('-')[0];
    if (!groups.has(prefix)) groups.set(prefix, []);
    groups.get(prefix).push(f);
  }
  const mockupItems = [...groups.entries()].flatMap(([prefix, files]) => {
    const heading = files.length > 1
      ? [`<h3>${esc(titleCase(prefix))}</h3>`] : [];
    return [...heading, ...files.map((f) => {
      const base = f.name.replace(/\.html$/i, '');
      return `<li><a class="card" href="/mockups/${encodeURIComponent(f.name)}">` +
        `${esc(titleCase(base))}<span class="stamp">updated ${esc(stamp(f.mtime))}</span></a></li>`;
    })];
  });

  // Screenshots: everything in test-artifacts/mockups/*.png, newest first,
  // linked with an inline lazy thumbnail.
  const shots = globWithStat(path.join(ARTIFACTS_ROOT, 'mockups'), '.png')
    .map((f) => {
      const u = `/artifacts/mockups/${encodeURIComponent(f.name)}`;
      return `<li><a class="card" href="${u}">${esc(f.name)}` +
        `<span class="stamp">updated ${esc(stamp(f.mtime))}</span></a>` +
        `<a href="${u}" style="border:none;padding:0">` +
        `<img class="thumb" loading="lazy" src="${u}" alt="${esc(f.name)}"></a></li>`;
    });

  const section = (title, items) =>
    `<h2>${title}</h2>${items.length
      ? `<ul class="files">${items.join('')}</ul>`
      : '<p class="empty">Nothing here yet.</p>'}`;

  return page('Whentorun mockup review',
    `<h1>Whentorun mockup review</h1>
     <p class="note">auto-refreshes from disk &mdash; reload to see the latest</p>
     ${section('Mockups', mockupItems)}${section('Screenshots', shots)}`);
}

// ------------------------------------------------------------------ files
// Multi-segment safe resolve: decode, reject dot-segments/backslashes/hidden
// files, and require the resolved path to stay inside root.
function safeResolve(root, rawRel) {
  let rel;
  try { rel = decodeURIComponent(rawRel); } catch { return null; }
  if (!rel || rel.includes('\\') || rel.includes('\0')) return null;
  const segs = rel.split('/').filter((s) => s.length);
  if (!segs.length || segs.some((s) => s === '.' || s === '..' || s.startsWith('.'))) return null;
  const p = path.resolve(root, segs.join('/'));
  return p.startsWith(root + path.sep) ? p : null;
}

function serveFile(req, res, filePath) {
  let st;
  try { st = fs.statSync(filePath); if (!st.isFile()) throw 0; } catch { return notFound(res); }
  const ctype = TYPES[path.extname(filePath).toLowerCase()] || 'application/octet-stream';
  const base = { 'Content-Type': ctype, 'Accept-Ranges': 'bytes' };
  const m = /^bytes=(\d*)-(\d*)$/.exec(req.headers.range || '');
  if (m && (m[1] !== '' || m[2] !== '')) {
    let start, end;
    if (m[1] === '') { start = Math.max(0, st.size - Number(m[2])); end = st.size - 1; }
    else { start = Number(m[1]); end = m[2] === '' ? st.size - 1 : Math.min(Number(m[2]), st.size - 1); }
    if (start >= st.size || start > end) {
      res.writeHead(416, { 'Content-Range': `bytes */${st.size}` });
      return res.end();
    }
    res.writeHead(206, { ...base, 'Content-Range': `bytes ${start}-${end}/${st.size}`, 'Content-Length': end - start + 1 });
    if (req.method === 'HEAD') return res.end();
    return fs.createReadStream(filePath, { start, end }).pipe(res);
  }
  res.writeHead(200, { ...base, 'Content-Length': st.size });
  if (req.method === 'HEAD') return res.end();
  fs.createReadStream(filePath).pipe(res);
}

const notFound = (res) => { res.writeHead(404, { 'Content-Type': 'text/plain' }); res.end('not found'); };

// ----------------------------------------------------------------- server
const server = http.createServer((req, res) => {
  if (req.method !== 'GET' && req.method !== 'HEAD') { res.writeHead(405); return res.end(); }
  const url = new URL(req.url, `http://${req.headers.host || 'x'}`);

  if (url.pathname === '/') {
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    return res.end(indexPage());
  }

  if (url.pathname.startsWith('/artifacts/')) {
    const p = safeResolve(ARTIFACTS_ROOT, url.pathname.slice('/artifacts/'.length));
    return p ? serveFile(req, res, p) : notFound(res);
  }

  // Everything else resolves against web/ as document root (/mockups/*, /static/*).
  const p = safeResolve(WEB_ROOT, url.pathname);
  return p ? serveFile(req, res, p) : notFound(res);
});

server.listen(PORT, HOST, () => {
  console.log(`whentorun review server on http://${HOST}:${PORT}/ (tailnet only)`);
});
