/* PDF 阅读器：由父页面通过 postMessage 控制页码与主题。
 * 宽屏「双页」模式下一次并排渲染相邻两页。 */
(() => {
  'use strict';

  const canvas1 = document.getElementById('pdf-page');
  const canvas2 = document.getElementById('pdf-page-2');
  const stage = document.getElementById('stage');
  const status = document.getElementById('status');
  const indicator = document.getElementById('indicator');

  const match = location.pathname.match(/^\/api\/books\/([\w-]+)\/pdfview/);
  const bookId = match ? match[1] : '';
  const params = new URLSearchParams(location.search);
  const startPage = Math.max(1, parseInt(params.get('page') || '1', 10) || 1);
  const spread = params.get('spread') === '1';
  const langParam = params.get('lang') || 'zh';
  const lang = ['zh', 'en', 'ja', 'ko'].includes(langParam) ? langParam : 'zh';
  const TEXT = {
    zh: { title: 'PDF 阅读', loading: '正在加载 PDF…', loadFailed: 'PDF 加载失败', renderFailed: '页面渲染失败', unknown: '未知错误' },
    en: { title: 'PDF reader', loading: 'Loading PDF…', loadFailed: 'Failed to load PDF', renderFailed: 'Failed to render page', unknown: 'Unknown error' },
    ja: { title: 'PDF リーダー', loading: 'PDF を読み込み中…', loadFailed: 'PDF の読み込みに失敗しました', renderFailed: 'ページの描画に失敗しました', unknown: '不明なエラー' },
    ko: { title: 'PDF 리더', loading: 'PDF 불러오는 중…', loadFailed: 'PDF를 불러오지 못했습니다', renderFailed: '페이지를 렌더링하지 못했습니다', unknown: '알 수 없는 오류' },
  }[lang];
  document.documentElement.lang = lang === 'zh' ? 'zh-CN' : lang;
  document.title = TEXT.title;
  status.textContent = TEXT.loading;

  let pdf = null;
  let current = startPage;
  let total = 0;
  let rendering = false;
  let pending = null;
  let lastWheel = 0;

  pdfjsLib.GlobalWorkerOptions.workerSrc = '/assets/pdfjs/pdf.worker.min.js';

  function post(type, payload) {
    parent.postMessage({ type, ...(payload || {}) }, '*');
  }

  function fail(message) {
    status.style.display = 'block';
    status.textContent = message;
    indicator.textContent = '';
  }

  if (!bookId) {
    fail('无法识别书籍');
    return;
  }

  pdfjsLib.getDocument({
    url: `/api/books/${bookId}/pdf`,
    cMapUrl: '/assets/pdfjs/cmaps/',
    cMapPacked: true,
    standardFontDataUrl: '/assets/pdfjs/standard_fonts/',
  }).promise.then((doc) => {
    pdf = doc;
    total = doc.numPages;
    status.style.display = 'none';
    post('moyue-pdf-ready', { pages: total, spread });
    render(startPage);
  }).catch((err) => {
    console.error(err);
    fail(TEXT.loadFailed + '：' + (err && err.message ? err.message : TEXT.unknown));
    post('moyue-pdf-ready', { pages: 0, error: String(err && err.message || err) });
  });

  function pagesFor(target) {
    if (!spread) return [target];
    const list = [target];
    if (target + 1 <= total) list.push(target + 1);
    return list;
  }

  async function drawPage(pageNumber, canvas, availW, availH) {
    const page = await pdf.getPage(pageNumber);
    const base = page.getViewport({ scale: 1 });
    const scale = Math.min(availW / base.width, availH / base.height);
    const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
    const viewport = page.getViewport({ scale: scale * dpr });

    canvas.width = Math.floor(viewport.width);
    canvas.height = Math.floor(viewport.height);
    canvas.style.width = `${Math.floor(viewport.width / dpr)}px`;
    canvas.style.height = `${Math.floor(viewport.height / dpr)}px`;
    canvas.hidden = false;

    const ctx = canvas.getContext('2d', { alpha: false });
    ctx.fillStyle = '#ffffff';
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    await page.render({ canvasContext: ctx, viewport }).promise;
  }

  async function render(pageNumber) {
    if (!pdf) return;
    const target = Math.min(Math.max(1, pageNumber), total);
    if (rendering) {
      pending = target;
      return;
    }
    rendering = true;
    current = target;

    try {
      const list = pagesFor(target);
      const gap = list.length > 1 ? 10 : 0;
      const availW = Math.max(120, (stage.clientWidth - 16 - gap * (list.length - 1)) / list.length);
      const availH = Math.max(120, stage.clientHeight - 16);

      await drawPage(list[0], canvas1, availW, availH);
      if (list[1]) await drawPage(list[1], canvas2, availW, availH);
      else {
        canvas2.hidden = true;
        canvas2.width = 0;
        canvas2.height = 0;
      }

      const last = list[list.length - 1];
      indicator.textContent = list.length > 1 ? `${list[0]}–${last} / ${total}` : `${target} / ${total}`;
      status.style.display = 'none';
      post('moyue-pdf-page', { page: target, pages: total, spread: list.length > 1 });
    } catch (err) {
      console.error(err);
      fail(TEXT.renderFailed + '：' + (err && err.message ? err.message : TEXT.unknown));
    } finally {
      rendering = false;
      if (pending != null && pending !== current) {
        const next = pending;
        pending = null;
        render(next);
      } else {
        pending = null;
      }
    }
  }

  function turn(delta) {
    if (!pdf) return;
    const step = spread ? 2 : 1;
    const next = current + delta * step;
    if (next < 1 || next > total) return;
    render(next);
  }

  window.addEventListener('message', (event) => {
    const data = event.data;
    if (!data || typeof data.type !== 'string') return;
    if (data.type === 'moyue-pdf-go') {
      render(Number(data.page) || 1);
    } else if (data.type === 'moyue-pdf-theme') {
      const theme = data.theme || {};
      const root = document.documentElement;
      if (theme.bg) root.style.setProperty('--bg', theme.bg);
      if (theme.ink) root.style.setProperty('--ink', theme.ink);
      if (theme.muted) root.style.setProperty('--muted', theme.muted);
      document.body.style.background = theme.bg || '';
    }
  });

  window.addEventListener('keydown', (event) => {
    if (event.key === 'ArrowRight' || event.key === 'ArrowDown' || event.key === 'PageDown' || event.key === ' ') {
      event.preventDefault();
      turn(1);
    } else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp' || event.key === 'PageUp') {
      event.preventDefault();
      turn(-1);
    }
  });

  canvas1.addEventListener('wheel', (event) => {
    const now = Date.now();
    if (now - lastWheel < 380) return;
    if (Math.abs(event.deltaY) < 12) return;
    lastWheel = now;
    turn(event.deltaY > 0 ? 1 : -1);
  }, { passive: true });
  canvas2.addEventListener('wheel', (event) => {
    const now = Date.now();
    if (now - lastWheel < 380) return;
    if (Math.abs(event.deltaY) < 12) return;
    lastWheel = now;
    turn(event.deltaY > 0 ? 1 : -1);
  }, { passive: true });

  let touchX = 0;
  let touchY = 0;
  window.addEventListener('touchstart', (event) => {
    const t = event.touches[0];
    touchX = t.clientX;
    touchY = t.clientY;
  }, { passive: true });
  window.addEventListener('touchend', (event) => {
    const t = event.changedTouches[0];
    const dx = t.clientX - touchX;
    const dy = t.clientY - touchY;
    if (Math.abs(dx) > 46 && Math.abs(dx) > Math.abs(dy) * 1.2) {
      turn(dx < 0 ? 1 : -1);
    }
  }, { passive: true });

  let resizeTimer = 0;
  window.addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      if (pdf) render(current);
    }, 180);
  });
})();
