/* 墨阅 Service Worker
 * 只缓存应用外壳与静态资源；/api/ 下的登录态数据一律走网络，
 * 避免在共用设备上把某个账号的书籍或进度写入缓存。
 */

const VERSION = 'moyue-v15';
const CACHE = `moyue-shell-${VERSION}`;
const OFFLINE_URL = '/offline.html';

const PRECACHE = [
  OFFLINE_URL,
  '/manifest.webmanifest',
  '/assets/app.css',
  '/assets/app.js',
  '/assets/i18n.js',
  '/assets/audioview.js',
  '/assets/icons/icon-192.png',
  '/assets/icons/icon-512.png',
  '/assets/icons/icon-maskable-512.png',
  '/assets/icons/apple-touch-icon.png',
  '/assets/icons/favicon-32.png',
];

self.addEventListener('install', (event) => {
  event.waitUntil((async () => {
    const cache = await caches.open(CACHE);
    await Promise.all(PRECACHE.map(async (url) => {
      try {
        const res = await fetch(new Request(url, { cache: 'reload' }));
        if (res.ok) await cache.put(url, res);
      } catch {
        /* 单个资源失败不影响其余缓存 */
      }
    }));
    await self.skipWaiting();
  })());
});

self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const keys = await caches.keys();
    await Promise.all(keys
      .filter((key) => key.startsWith('moyue-shell-') && key !== CACHE)
      .map((key) => caches.delete(key)));
    await self.clients.claim();
  })());
});

self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'SKIP_WAITING') self.skipWaiting();
});

self.addEventListener('fetch', (event) => {
  const request = event.request;
  if (request.method !== 'GET') return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname.startsWith('/api/')) return;

  // 页面导航：优先最新页面；服务器不可达时给出离线说明页。
  if (request.mode === 'navigate') {
    event.respondWith((async () => {
      try {
        return await fetch(request);
      } catch {
        const cache = await caches.open(CACHE);
        return (await cache.match(request)) || (await cache.match(OFFLINE_URL)) ||
          new Response('当前离线，且没有可用的离线页面。', {
            status: 503,
            headers: { 'Content-Type': 'text/plain; charset=utf-8' },
          });
      }
    })());
    return;
  }

  // 静态资源：网络优先，失败时回落到安装时缓存。
  const isAsset = url.pathname.startsWith('/assets/') || url.pathname === '/manifest.webmanifest';
  if (!isAsset) return;

  event.respondWith((async () => {
    try {
      const res = await fetch(request);
      if (res && res.ok) {
        const cache = await caches.open(CACHE);
        cache.put(request, res.clone()).catch(() => {});
      }
      return res;
    } catch {
      const cached = await (await caches.open(CACHE)).match(request);
      if (cached) return cached;
      return new Response('', { status: 504, statusText: 'Offline' });
    }
  })());
});
