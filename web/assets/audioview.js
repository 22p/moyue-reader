(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const audio = $('audio');
  const seek = $('seek');
  const playBtn = $('play');
  const playIcon = $('playIcon');
  const titleEl = $('title');
  const authorEl = $('author');
  const curEl = $('current');
  const durEl = $('duration');
  const errorEl = $('error');

  const id = (location.pathname.match(/\/api\/books\/([^/]+)\/audioview/) || [])[1];
  const params = new URLSearchParams(location.search);
  const startFraction = Math.max(0, Math.min(0.995, Number(params.get('start')) || 0));
  const langParam = params.get('lang') || 'zh';
  const lang = ['zh', 'en', 'ja', 'ko'].includes(langParam) ? langParam : 'zh';
  const TEXT = {
    zh: {
      fallback: '有声书',
      hint: '播放进度会自动保存',
      error: '无法播放该音频文件，请确认格式受浏览器支持。',
      play: '播放',
      pause: '暂停',
      back: '后退 15 秒',
      forward: '前进 15 秒',
      seek: '播放进度',
    },
    en: {
      fallback: 'Audiobook',
      hint: 'Playback progress is saved automatically',
      error: 'Unable to play this audio file. Check the format.',
      play: 'Play',
      pause: 'Pause',
      back: 'Back 15 seconds',
      forward: 'Forward 15 seconds',
      seek: 'Playback progress',
    },
    ja: {
      fallback: 'オーディオブック',
      hint: '再生位置は自動的に保存されます',
      error: 'この音声ファイルを再生できません。形式を確認してください。',
      play: '再生',
      pause: '一時停止',
      back: '15秒戻る',
      forward: '15秒進む',
      seek: '再生位置',
    },
    ko: {
      fallback: '오디오북',
      hint: '재생 위치가 자동으로 저장됩니다',
      error: '이 오디오 파일을 재생할 수 없습니다. 형식을 확인하세요.',
      play: '재생',
      pause: '일시정지',
      back: '15초 뒤로',
      forward: '15초 앞으로',
      seek: '재생 위치',
    },
  }[lang];
  document.documentElement.lang = lang === 'zh' ? 'zh-CN' : lang;
  if (id) audio.src = `/api/books/${id}/audio`;

  const title = params.get('title') || TEXT.fallback;
  const author = params.get('author') || '';
  titleEl.textContent = title;
  authorEl.textContent = author;
  document.title = title + ' · ' + (lang === 'zh' ? '墨阅' : 'Moyue');
  document.querySelector('.hint').textContent = TEXT.hint;
  document.getElementById('error').textContent = TEXT.error;
  seek.setAttribute('aria-label', TEXT.seek);
  document.getElementById('back').setAttribute('aria-label', TEXT.back);
  document.getElementById('forward').setAttribute('aria-label', TEXT.forward);

  let duration = 0;
  let lastPost = 0;
  let seeking = false;
  let started = false;

  const fmt = (sec) => {
    if (!Number.isFinite(sec) || sec < 0) return '--:--';
    const s = Math.floor(sec % 60);
    const m = Math.floor(sec / 60) % 60;
    const h = Math.floor(sec / 3600);
    return h > 0
      ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
      : `${m}:${String(s).padStart(2, '0')}`;
  };

  const post = (type, data = {}) => {
    try {
      if (window.parent && window.parent !== window) {
        window.parent.postMessage({ type, ...data }, '*');
      }
    } catch { /* 忽略 */ }
  };

  const setFill = (fraction) => {
    seek.value = String(Math.round(fraction * 1000));
    seek.style.setProperty('--fill', `${Math.round(fraction * 100)}%`);
  };

  const updateTime = () => {
    const d = Number.isFinite(audio.duration) ? audio.duration : duration;
    const t = audio.currentTime || 0;
    curEl.textContent = fmt(t);
    durEl.textContent = d > 0 ? fmt(d) : '--:--';
    if (!seeking) setFill(d > 0 ? Math.min(1, t / d) : 0);
    return { t, d };
  };

  const announce = (force = false) => {
    const { t, d } = updateTime();
    const now = Date.now();
    if (!force && now - lastPost < 5000) return;
    lastPost = now;
    if (d > 0) post('moyue-audio-time', { currentTime: t, duration: d, fraction: Math.min(1, t / d) });
  };

  const setPlaying = (playing) => {
    playIcon.innerHTML = playing
      ? '<path d="M7 5h4v14H7zM13 5h4v14h-4z"/>'
      : '<path d="M8 5l11 7-11 7z"/>';
    playBtn.setAttribute('aria-label', playing ? TEXT.pause : TEXT.play);
  };

  const toggle = async () => {
    if (!audio.src) return;
    try {
      if (audio.paused) {
        await audio.play();
      } else {
        audio.pause();
      }
    } catch {
      errorEl.style.display = 'block';
    }
  };

  playBtn.addEventListener('click', toggle);
  $('back').addEventListener('click', () => {
    audio.currentTime = Math.max(0, audio.currentTime - 15);
    announce(true);
  });
  $('forward').addEventListener('click', () => {
    audio.currentTime = Math.min(duration || audio.duration || 0, audio.currentTime + 15);
    announce(true);
  });

  seek.addEventListener('input', () => {
    seeking = true;
    setFill(Number(seek.value) / 1000);
  });
  seek.addEventListener('change', () => {
    const d = duration || audio.duration;
    if (Number.isFinite(d) && d > 0) {
      audio.currentTime = (Number(seek.value) / 1000) * d;
    }
    seeking = false;
    announce(true);
  });

  audio.addEventListener('loadedmetadata', () => {
    duration = Number.isFinite(audio.duration) ? audio.duration : 0;
    if (duration > 0 && startFraction > 0) {
      audio.currentTime = startFraction * duration;
    }
    updateTime();
    post('moyue-audio-ready', { duration });
    announce(true);
  });

  audio.addEventListener('timeupdate', () => announce());
  audio.addEventListener('play', () => { started = true; setPlaying(true); });
  audio.addEventListener('pause', () => { setPlaying(false); announce(true); });
  audio.addEventListener('ended', () => {
    setPlaying(false);
    setFill(1);
    post('moyue-audio-time', { currentTime: duration, duration, fraction: 1 });
  });
  audio.addEventListener('error', () => {
    errorEl.style.display = 'block';
    post('moyue-audio-error', { message: '无法播放音频' });
  });

  document.addEventListener('keydown', (e) => {
    if (e.target && /input|textarea/i.test(e.target.tagName)) return;
    if (e.code === 'Space') { e.preventDefault(); toggle(); }
    else if (e.code === 'ArrowLeft') { e.preventDefault(); $('back').click(); }
    else if (e.code === 'ArrowRight') { e.preventDefault(); $('forward').click(); }
  });

  window.addEventListener('message', (event) => {
    const data = event.data;
    if (!data || typeof data.type !== 'string') return;
    if (data.type === 'moyue-audio-seek') {
      const d = duration || audio.duration;
      if (Number.isFinite(d) && d > 0) {
        audio.currentTime = Math.max(0, Math.min(1, Number(data.fraction) || 0)) * d;
        updateTime();
        announce(true);
      }
    } else if (data.type === 'moyue-audio-theme' && data.theme) {
      const t = data.theme;
      const root = document.documentElement.style;
      if (t.bg) root.setProperty('--bg', t.bg);
      if (t.card) root.setProperty('--card', t.card);
      if (t.ink) root.setProperty('--ink', t.ink);
      if (t.muted) root.setProperty('--muted', t.muted);
      if (t.line) root.setProperty('--line', t.line);
      if (t.accent) root.setProperty('--accent', t.accent);
      if (t.accentInk) root.setProperty('--accent-ink', t.accentInk);
    }
  });

  if (!id) {
    errorEl.style.display = 'block';
  }
  setPlaying(false);
})();
