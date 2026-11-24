/* MControl MDesign PWA Service Worker v2 */
const CACHE_NAME = 'mcontrol-pwa-v2';
const PRECACHE_URLS = [
  './',
  './manifest.json',
  './saas-dnd.js',
  './icon-192.png',
  './icon-512.png',
  './icon-maskable-512.png',
  './apple-touch-icon.png'
];

const OFFLINE_HTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#090f1d">
<title>MControl — Offline</title>
<style>
  *{box-sizing:border-box;margin:0;padding:0}
  body{min-height:100vh;display:flex;align-items:center;justify-content:center;padding:20px;background:radial-gradient(circle at 25% 20%,rgba(34,211,238,.16),transparent 45%),radial-gradient(circle at 80% 80%,rgba(244,114,182,.15),transparent 45%),#090f1d;color:#e2e8f0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;text-align:center}
  .card{max-width:390px;width:100%;padding:32px 26px;border-radius:24px;background:rgba(15,23,42,.82);border:1.5px solid rgba(129,140,248,.34);backdrop-filter:blur(18px);box-shadow:0 24px 60px rgba(0,0,0,.55);animation:pop .4s cubic-bezier(.16,1,.3,1)}
  @keyframes pop{from{opacity:0;transform:translateY(14px) scale(.96)}to{opacity:1;transform:none}}
  .logo{width:64px;height:64px;margin:0 auto 14px;display:block}
  h2{font-size:1.2rem;margin-bottom:8px;color:#fff}
  p{font-size:.86rem;line-height:1.6;color:#94a3b8;margin-bottom:20px}
  button{padding:11px 24px;border-radius:999px;border:none;background:linear-gradient(135deg,#22d3ee,#818cf8);color:#090f1d;font-weight:800;font-size:.88rem;cursor:pointer;transition:transform .16s ease}
  button:active{transform:scale(.96)}
</style>
</head>
<body>
  <div class="card">
    <svg class="logo" viewBox="0 0 64 64" fill="none">
      <defs><linearGradient id="og" x1="8" y1="8" x2="56" y2="56" gradientUnits="userSpaceOnUse"><stop offset="0%" stop-color="#22d3ee"/><stop offset="50%" stop-color="#818cf8"/><stop offset="100%" stop-color="#f472b6"/></linearGradient></defs>
      <rect width="64" height="64" rx="16" fill="#0f172a"/>
      <path d="M10 52V20c0-8.5 10.5-11.5 15.5-4.5L32 27.5l6.5-12C43.5 8.5 54 11.5 54 20v32" stroke="url(#og)" stroke-width="7.4" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>
    <h2>Connection Lost / ارتباط قطع شد</h2>
    <p>Cannot reach your MControl panel right now. Check your internet or VPN connection and retry.</p>
    <button onclick="location.reload()">↻ Retry Connection</button>
  </div>
</body>
</html>`;

self.addEventListener('install', event => {
  event.waitUntil(
    caches.open(CACHE_NAME).then(cache =>
      Promise.allSettled(PRECACHE_URLS.map(u => cache.add(new Request(u, { cache: 'reload' }))))
    )
  );
});

self.addEventListener('activate', event => {
  event.waitUntil(
    caches.keys().then(keys =>
      Promise.all(keys.filter(k => k !== CACHE_NAME).map(k => caches.delete(k)))
    ).then(() => self.clients.claim())
  );
});

self.addEventListener('message', event => {
  if (event.data && event.data.type === 'SKIP_WAITING') {
    self.skipWaiting();
  }
});

self.addEventListener('fetch', event => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname.includes('/api/') || url.pathname.includes('/_mc_dist/')) return;

  // Navigation requests: Network-First -> Cache -> Branded Offline Fallback
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req).then(resp => {
        if (resp && resp.ok) {
          const clone = resp.clone();
          caches.open(CACHE_NAME).then(c => c.put(req, clone)).catch(() => {});
        }
        return resp;
      }).catch(async () => {
        const cached = await caches.match(req) || await caches.match('./');
        return cached || new Response(OFFLINE_HTML, {
          status: 503,
          headers: { 'Content-Type': 'text/html; charset=utf-8' }
        });
      })
    );
    return;
  }

  // Static assets (icons, manifest, js): Stale-While-Revalidate
  event.respondWith(
    caches.match(req).then(cached => {
      const networkFetch = fetch(req).then(resp => {
        if (resp && resp.ok) {
          const clone = resp.clone();
          caches.open(CACHE_NAME).then(c => c.put(req, clone)).catch(() => {});
        }
        return resp;
      }).catch(() => cached);
      return cached || networkFetch;
    })
  );
});
