/* 墨阅 · 书库前端 */
(() => {
  'use strict';

  /* ============================ 工具 ============================ */

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
  const clamp = (n, min, max) => Math.min(max, Math.max(min, n));
  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
  const tr = (s) => (window.I18N ? window.I18N.translate(String(s)) : String(s));

  const THEMES = {
    paper: { pageBg: '#f7f4ec', ink: '#262119', link: '#2f5d50', sel: 'rgba(47,93,80,.2)', ui: '#f4f1ea' },
    sepia: { pageBg: '#f3e7d0', ink: '#3a2f1e', link: '#8a5a2b', sel: 'rgba(138,90,43,.2)', ui: '#efe3ca' },
    mint: { pageBg: '#eef4ef', ink: '#1f2b26', link: '#2c6656', sel: 'rgba(44,102,86,.2)', ui: '#e9f0eb' },
    night: { pageBg: '#16181c', ink: '#d8d2c6', link: '#8abdaa', sel: 'rgba(138,189,170,.22)', ui: '#131518' },
  };

  const FONTS = {
    serif: '"Songti SC","Source Han Serif SC","Noto Serif CJK SC","STSong",Georgia,serif',
    sans: '"PingFang SC","Hiragino Sans GB","Microsoft YaHei","Segoe UI",system-ui,sans-serif',
    kai: '"Kaiti SC","STKaiti","KaiTi","Noto Serif CJK SC",serif',
  };

  const normalizeLang = (value) => (['en', 'ja', 'ko'].includes(value) ? value : 'zh');

  function detectLang() {
    const nav = (navigator.language || '').toLowerCase();
    if (nav.startsWith('ja')) return 'ja';
    if (nav.startsWith('ko')) return 'ko';
    if (nav.startsWith('en')) return 'en';
    return 'zh';
  }

  const DEFAULT_PREFS = {
    theme: 'paper', font: 'sans', size: 19, line: 1.9, measure: 760, mode: 'scroll',
    ttsVoice: 'mimo_default', ttsRate: 1,
    lang: detectLang(),
  };

  function loadPrefs() {
    try {
      return { ...DEFAULT_PREFS, ...JSON.parse(localStorage.getItem('moyue.prefs') || '{}') };
    } catch {
      return { ...DEFAULT_PREFS };
    }
  }

  const prefs = loadPrefs();

  function savePrefs() {
    try { localStorage.setItem('moyue.prefs', JSON.stringify(prefs)); } catch { /* 忽略 */ }
  }

  function fmtDuration(totalSeconds) {
    const s = Math.max(0, Math.round(totalSeconds || 0));
    if (s === 0) return { value: 0, unit: '分钟' };
    if (s < 60) return { value: s, unit: '秒' };
    const m = Math.round(s / 60);
    if (m < 60) return { value: m, unit: '分钟' };
    const h = Math.floor(m / 60);
    const rm = m % 60;
    return { value: h, unit: rm ? `小时 ${rm} 分` : '小时' };
  }

  function relativeTime(iso) {
    if (!iso) return '';
    const diff = (Date.now() - new Date(iso).getTime()) / 1000;
    if (diff < 60) return '刚刚';
    if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
    if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
    if (diff < 172800) return '昨天';
    if (diff < 86400 * 30) return `${Math.floor(diff / 86400)} 天前`;
    return new Date(iso).toLocaleDateString('zh-CN');
  }

  const isFixed = (book) => !!book && (book.type === 'pdf' || book.type === 'cbz' || book.type === 'cbr');
  const isAudio = (book) => !!book && book.type === 'audio';
  const isTextBook = (book) => !!book && !isFixed(book) && !isAudio(book);
  const SPREAD_MIN = 960;
  const isSpreadWide = () => !!el.stage && el.stage.clientWidth >= SPREAD_MIN;
  const isPaginated = () => isTextBook(state.current) && prefs.mode !== 'scroll';
  const typeLabel = (type) => ({
    txt: 'TXT', pdf: 'PDF', cbz: 'CBZ', cbr: 'CBR', fb2: 'FB2',
    mobi: 'MOBI', azw: 'AZW', azw3: 'AZW3', prc: 'PRC', audio: 'AUDIO',
  }[type] || '');

  function hashHue(str) {
    let h = 0;
    for (let i = 0; i < str.length; i++) h = (h * 31 + str.charCodeAt(i)) % 360;
    return (h * 7) % 360;
  }

  class ApiError extends Error {
    constructor(message, status) {
      super(message);
      this.status = status;
    }
  }

  async function request(url, options = {}) {
    const opts = { ...options, credentials: 'same-origin' };
    if (opts.body && !(opts.body instanceof FormData)) {
      opts.headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) };
      opts.body = JSON.stringify(opts.body);
    }
    const res = await fetch(url, opts);
    if (!res.ok) {
      let message = `请求失败（${res.status}）`;
      try {
        const data = await res.json();
        if (data && data.error) message = data.error;
      } catch { /* 忽略 */ }
      if (res.status === 401 && !url.startsWith('/api/auth/login') && !url.startsWith('/api/auth/setup')) {
        handleSessionExpired();
      }
      throw new ApiError(message, res.status);
    }
    if (res.status === 204) return null;
    return res.json();
  }

  const api = {
    authState: () => request('/api/auth/state'),
    login: (body) => request('/api/auth/login', { method: 'POST', body }),
    setup: (body) => request('/api/auth/setup', { method: 'POST', body }),
    logout: () => request('/api/auth/logout', { method: 'POST' }),
    changePassword: (body) => request('/api/auth/password', { method: 'POST', body }),
    libraries: () => request('/api/libraries'),
    books: () => request('/api/books'),
    stats: () => request('/api/stats'),
    detail: (id) => request(`/api/books/${id}`),
    remove: (id) => request(`/api/books/${id}`, { method: 'DELETE' }),
    progress: (id, body) => request(`/api/books/${id}/progress`, { method: 'PUT', body }),
    resetProgress: (id) => request(`/api/books/${id}/progress`, { method: 'DELETE' }),
    read: (id, seconds) => request(`/api/books/${id}/read`, { method: 'POST', body: { seconds } }),
    addBookmark: (id, body) => request(`/api/books/${id}/bookmarks`, { method: 'POST', body }),
    removeBookmark: (id, bid) => request(`/api/books/${id}/bookmarks/${bid}`, { method: 'DELETE' }),
    addAnnotation: (id, body) => request(`/api/books/${id}/annotations`, { method: 'POST', body }),
    updateAnnotation: (id, aid, body) => request(`/api/books/${id}/annotations/${aid}`, { method: 'PATCH', body }),
    removeAnnotation: (id, aid) => request(`/api/books/${id}/annotations/${aid}`, { method: 'DELETE' }),
    search: (id, q) => request(`/api/books/${id}/search?q=${encodeURIComponent(q)}`),
    sessions: () => request('/api/auth/sessions'),
    revokeSession: (id) => request(`/api/auth/sessions/${id}`, { method: 'DELETE' }),
    revokeOtherSessions: () => request('/api/auth/sessions/revoke-others', { method: 'POST' }),
    adminLibraries: () => request('/api/admin/libraries'),
    createLibrary: (body) => request('/api/admin/libraries', { method: 'POST', body }),
    deleteLibrary: (id) => request(`/api/admin/libraries/${id}`, { method: 'DELETE' }),
    scanLibrary: (id) => request(`/api/admin/libraries/${id}/scan`, { method: 'POST' }),
    adminUsers: () => request('/api/admin/users'),
    createUser: (body) => request('/api/admin/users', { method: 'POST', body }),
    updateUser: (id, body) => request(`/api/admin/users/${id}`, { method: 'PATCH', body }),
    deleteUser: (id) => request(`/api/admin/users/${id}`, { method: 'DELETE' }),
    ttsStatus: () => request('/api/tts/status'),
    adminTTS: () => request('/api/admin/settings/tts'),
    saveAdminTTS: (body) => request('/api/admin/settings/tts', { method: 'PUT', body }),
  };

  /* ============================ DOM ============================ */

  const el = {
    authView: $('#authView'),
    authLang: $('#authLang'),
    authForm: $('#authForm'),
    authEyebrow: $('#authEyebrow'),
    authTitle: $('#authTitle'),
    authDesc: $('#authDesc'),
    authUser: $('#authUser'),
    authPass: $('#authPass'),
    authConfirmWrap: $('#authConfirmWrap'),
    authConfirm: $('#authConfirm'),
    authError: $('#authError'),
    authSubmit: $('#authSubmit'),
    authHint: $('#authHint'),

    library: $('#libraryView'),
    admin: $('#adminView'),
    reader: $('#readerView'),
    topbar: $('.topbar'),
    search: $('#searchInput'),
    sort: $('#sortSelect'),
    uploadBtn: $('#uploadBtn'),
    emptyUpload: $('#emptyUpload'),
    emptyHint: $('#emptyHint'),
    uploadScrim: $('#uploadScrim'),
    uploadClose: $('#uploadClose'),
    uploadLib: $('#uploadLib'),
    uploadDrop: $('#uploadDrop'),
    uploadPick: $('#uploadPick'),
    uploadList: $('#uploadList'),
    uploadStart: $('#uploadStart'),
    uploadCancel: $('#uploadCancel'),
    uploadSummary: $('#uploadSummary'),
    uploadPercent: $('#uploadPercent'),
    uploadTotalBar: $('#uploadTotalBar'),
    grid: $('#bookGrid'),
    empty: $('#emptyState'),
    shelfCount: $('#shelfCount'),
    shelfTitle: $('#shelfTitle'),
    folderTrail: $('#folderTrail'),
    overview: $('#overview'),
    continueCard: $('#continueCard'),
    librarySwitch: $('#librarySwitch'),

    userMenu: $('#userMenu'),
    userTheme: $('#userTheme'),
    userLang: $('#userLang'),
    avatarBtn: $('#avatarBtn'),
    avatarText: $('#avatarText'),
    userPop: $('#userPop'),
    userName: $('#userName'),
    userAccount: $('#userAccount'),
    adminEntry: $('#adminEntry'),
    installEntry: $('#installEntry'),

    adminBack: $('#adminBack'),
    adminWho: $('#adminWho'),
    adminAvatar: $('#adminAvatar'),
    adminLibsSection: $('#adminLibsSection'),
    adminUsersSection: $('#adminUsersSection'),
    libForm: $('#libForm'),
    libKind: $('#libKind'),
    libPathField: $('#libPathField'),
    libDavUrlField: $('#libDavUrlField'),
    libDavUserField: $('#libDavUserField'),
    libDavPassField: $('#libDavPassField'),
    libList: $('#libList'),
    refreshLibs: $('#refreshLibs'),
    userForm: $('#userForm'),
    userFormTitle: $('#userFormTitle'),
    userFormDesc: $('#userFormDesc'),
    userSubmit: $('#userSubmit'),
    userCancelEdit: $('#userCancelEdit'),
    userLibChecks: $('#userLibChecks'),
    userList: $('#userList'),
    refreshUsers: $('#refreshUsers'),

    frame: $('#frame'),
    stage: $('.stage'),
    rTitle: $('#rTitle'),
    rChapter: $('#rChapter'),
    backBtn: $('#backBtn'),
    tocBtn: $('#tocBtn'),
    ttsBtn: $('#ttsBtn'),
    markBtn: $('#markBtn'),
    settingsBtn: $('#settingsBtn'),
    fullBtn: $('#fullBtn'),
    prevBtn: $('#prevBtn'),
    nextBtn: $('#nextBtn'),
    range: $('#progressRange'),
    progressLabel: $('#progressLabel'),
    nextHint: $('#nextHint'),

    scrim: $('#scrim'),
    tocDrawer: $('#tocDrawer'),
    settingsDrawer: $('#settingsDrawer'),
    tocList: $('#tocList'),
    markList: $('#markList'),
    markCount: $('#markCount'),
    noteList: $('#noteList'),
    noteCount: $('#noteCount'),
    searchPane: $('#searchPane'),
    searchInputInBook: $('#searchInputInBook'),
    searchGo: $('#searchGo'),
    searchMeta: $('#searchMeta'),
    searchResults: $('#searchResults'),
    annoBar: $('#annoBar'),
    annoNote: $('#annoNote'),
    annoCancel: $('#annoCancel'),
    noteScrim: $('#noteScrim'),
    noteTitle: $('#noteTitle'),
    noteQuote: $('#noteQuote'),
    noteText: $('#noteText'),
    noteColors: $('#noteColors'),
    noteSave: $('#noteSave'),
    noteCancel: $('#noteCancel'),
    noteDelete: $('#noteDelete'),
    sessionScrim: $('#sessionScrim'),
    sessionList: $('#sessionList'),
    sessionClose: $('#sessionClose'),
    sessionRevokeOthers: $('#sessionRevokeOthers'),

    themeGrid: $('#themeGrid'),
    modeSeg: $('#modeSeg'),
    fontSeg: $('#fontSeg'),
    langSeg: $('#langSeg'),
    sizeRange: $('#sizeRange'),
    sizeVal: $('#sizeVal'),
    sizeUp: $('#sizeUp'),
    sizeDown: $('#sizeDown'),
    lineRange: $('#lineRange'),
    lineVal: $('#lineVal'),
    measureRange: $('#measureRange'),
    measureVal: $('#measureVal'),
    resetPrefs: $('#resetPrefs'),

    ttsBar: $('#ttsBar'),
    ttsStatus: $('#ttsStatus'),
    ttsPlay: $('#ttsPlay'),
    ttsPlayIcon: $('#ttsPlayIcon'),
    ttsPrev: $('#ttsPrev'),
    ttsNext: $('#ttsNext'),
    ttsVoice: $('#ttsVoice'),
    ttsRate: $('#ttsRate'),
    ttsClose: $('#ttsClose'),

    adminSettingsSection: $('#adminSettingsSection'),
    ttsForm: $('#ttsForm'),
    ttsProvider: $('#ttsProvider'),
    ttsAdminVoice: $('#ttsAdminVoice'),
    ttsBaseUrl: $('#ttsBaseUrl'),
    ttsModel: $('#ttsModel'),
    ttsApiKey: $('#ttsApiKey'),
    ttsKeyState: $('#ttsKeyState'),
    ttsStateTag: $('#ttsStateTag'),
    ttsSave: $('#ttsSave'),
    ttsTest: $('#ttsTest'),
    ttsClearKey: $('#ttsClearKey'),
    ttsTestResult: $('#ttsTestResult'),

    dropOverlay: $('#dropOverlay'),
    dropHint: $('#dropHint'),
    toasts: $('#toasts'),
    dialogScrim: $('#dialogScrim'),
    dialogTitle: $('#dialogTitle'),
    dialogText: $('#dialogText'),
    dialogOk: $('#dialogOk'),
    dialogCancel: $('#dialogCancel'),
    passwordScrim: $('#passwordScrim'),
    passwordForm: $('#passwordForm'),
    pwCurrent: $('#pwCurrent'),
    pwNew: $('#pwNew'),
    pwConfirm: $('#pwConfirm'),
    pwCancel: $('#pwCancel'),
  };

  const state = {
    user: null,
    authMode: 'login',
    view: 'auth',
    libraries: [],
    books: [],
    stats: null,
    selectedLibrary: 'all',
    folder: '',
    query: '',
    sort: 'recent',
    current: null,
    chapter: 0,
    fraction: 0,
    percent: 0,
    pendingFraction: 0,
    saveTimer: 0,
    frameFadeTimer: 0,
    heartbeat: 0,
    activeSeconds: 0,
    lastTick: 0,
    scrollRaf: 0,
    lastSaved: '',
    loading: false,
    pdf: { ready: false, loading: false, pages: 0, bookId: '', spread: false },
    comicSpread: false,
    page: { active: false, page: 0, pages: 1, vw: 0 },
    tts: {
      status: null, active: false, playing: false, loading: false,
      index: 0, part: 0, sentences: [], chapter: -1, audio: null, token: 0,
      cache: new Map(), prefetching: new Set(), hintShown: false, seeking: false,
    },
    upload: { items: [], running: false, xhr: null, cancelled: false, seq: 1, clearKey: false },
    audioReady: false,
    audioBook: '',
    selection: null,
    noteEditing: null,
    noteSelection: null,
    noteColor: 'yellow',
    bookmarkBusy: false,
    pendingAnnotation: '',
    pendingSearch: null,
    searchDone: false,
    searchSeq: 0,
    admin: { libraries: [], users: [], tab: 'libs', editUserId: null, poll: 0, tts: null, ttsClearKey: false },
  };

  /* ============================ 提示 & 弹窗 ============================ */

  function toast(message, { type = 'info', loading = false, duration = 3000 } = {}) {
    message = tr(message);
    const node = document.createElement('div');
    node.className = 'toast' + (type === 'error' ? ' is-error' : '');
    node.innerHTML = (loading ? '<span class="spinner"></span>' : '') + `<span>${esc(message)}</span>`;
    el.toasts.appendChild(node);
    let timer = 0;
    const close = () => {
      clearTimeout(timer);
      if (!node.isConnected) return;
      node.classList.add('is-out');
      setTimeout(() => node.remove(), 240);
    };
    const arm = (ms) => {
      clearTimeout(timer);
      if (ms) timer = setTimeout(close, ms);
    };
    arm(duration);
    return {
      close,
      update(text, opts = {}) {
        node.innerHTML = (opts.loading ? '<span class="spinner"></span>' : '') + `<span>${esc(text)}</span>`;
        node.classList.toggle('is-error', opts.type === 'error');
        arm(opts.duration ?? 2600);
      },
    };
  }

  let dialogResolve = null;

  function confirmDialog({ title = '确认操作', text = '', okText = '确定', danger = true } = {}) {
    el.dialogTitle.textContent = title;
    el.dialogText.textContent = text;
    el.dialogOk.textContent = okText;
    el.dialogOk.className = 'btn ' + (danger ? 'btn-danger' : 'btn-primary');
    el.dialogScrim.hidden = false;
    return new Promise((resolve) => { dialogResolve = resolve; });
  }

  function settleDialog(result) {
    el.dialogScrim.hidden = true;
    if (dialogResolve) {
      dialogResolve(result);
      dialogResolve = null;
    }
  }

  el.dialogOk.addEventListener('click', () => settleDialog(true));
  el.dialogCancel.addEventListener('click', () => settleDialog(false));
  el.dialogScrim.addEventListener('click', (e) => {
    if (e.target === el.dialogScrim) settleDialog(false);
  });

  /* ============================ 认证 ============================ */

  function setUser(user) {
    state.user = user;
    if (!user) return;
    const initial = (user.displayName || user.username || '墨').trim().slice(0, 1).toUpperCase();
    el.avatarText.textContent = initial;
    el.avatarBtn.title = user.displayName || user.username;
    el.userName.textContent = user.displayName || user.username;
    el.userAccount.textContent = '@' + user.username;
    el.adminEntry.hidden = !user.isAdmin;
    el.adminWho.textContent = user.displayName || user.username;
    el.adminAvatar.textContent = initial;
    el.uploadBtn.hidden = !user.isAdmin;
    el.emptyUpload.hidden = !user.isAdmin;
    el.emptyHint.hidden = !user.isAdmin;
  }

  function showAuth(mode = 'login', message = '') {
    stopHeartbeat();
    stopAdminPoll();
    if (state.tts.active) stopTTS();
    closeDrawers();
    closeMenus();
    el.userPop.hidden = true;

    state.view = 'auth';
    state.authMode = mode;
    state.folder = '';
    state.query = '';
    el.search.value = '';
    document.body.classList.remove('reading');
    el.authView.hidden = false;
    el.library.hidden = true;
    el.admin.hidden = true;
    el.reader.hidden = true;
    el.frame.src = 'about:blank';
    state.current = null;

    const isSetup = mode === 'setup';
    el.authEyebrow.textContent = isSetup ? '首次使用' : '欢迎回来';
    el.authTitle.textContent = isSetup ? '创建管理员账号' : '登录书库';
    el.authDesc.textContent = isSetup
      ? '这是第一次启动。创建的管理员账号可以管理书库与用户。'
      : '使用管理员为你创建的账号登录。';
    el.authConfirmWrap.hidden = !isSetup;
    el.authSubmit.textContent = isSetup ? '创建并进入' : '登录';
    el.authHint.textContent = isSetup
      ? '账号信息只保存在这台服务器上，请妥善保管密码。'
      : '忘记密码？请联系管理员重置。';
    el.authPass.autocomplete = isSetup ? 'new-password' : 'current-password';
    el.authForm.reset();
    el.authError.hidden = true;
    el.authError.textContent = message;
    if (message) el.authError.hidden = false;
    document.title = isSetup
      ? (prefs.lang === 'zh' ? '初始化 · 墨阅' : 'Setup · Moyue')
      : (prefs.lang === 'zh' ? '登录 · 墨阅' : 'Sign in · Moyue');
    if (window.I18N) window.I18N.setLang(normalizeLang(prefs.lang));
    setTimeout(() => el.authUser.focus(), 60);
  }

  async function submitAuth(event) {
    event.preventDefault();
    const username = el.authUser.value.trim();
    const password = el.authPass.value;
    const isSetup = state.authMode === 'setup';

    if (!username || !password) {
      showAuthMessage('请填写用户名和密码');
      return;
    }
    if (isSetup) {
      if (password.length < 6) return showAuthMessage('密码至少需要 6 个字符');
      if (password !== el.authConfirm.value) return showAuthMessage('两次输入的密码不一致');
    }

    el.authSubmit.disabled = true;
    el.authSubmit.textContent = isSetup ? '正在创建…' : '正在登录…';
    try {
      const data = isSetup
        ? await api.setup({ username, password, displayName: username })
        : await api.login({ username, password });
      setUser(data.user);
      el.authView.hidden = true;
      history.replaceState(null, '', location.hash.startsWith('#/') ? location.hash : '/');
      routeFromLocation();
    } catch (err) {
      showAuthMessage(err.message);
    } finally {
      el.authSubmit.disabled = false;
      el.authSubmit.textContent = isSetup ? '创建并进入' : '登录';
    }
  }

  function showAuthMessage(message) {
    el.authError.textContent = message;
    el.authError.hidden = !message;
  }

  function handleSessionExpired() {
    if (state.view === 'auth') return;
    setUser(null);
    showAuth('login', '登录状态已过期，请重新登录。');
    history.replaceState(null, '', '/');
  }

  async function logout() {
    try { await api.logout(); } catch { /* 忽略 */ }
    setUser(null);
    state.books = [];
    state.libraries = [];
    state.current = null;
    state.folder = '';
    state.query = '';
    el.search.value = '';
    el.userPop.hidden = true;
    history.replaceState(null, '', '/');
    showAuth('login');
  }

  /* ============================ 书库 ============================ */

  function enterLibrary({ updateHistory = true } = {}) {
    stopAdminPoll();
    if (state.tts.active) stopTTS();
    state.view = 'library';
    state.current = null;
    document.body.classList.remove('reading');
    el.authView.hidden = true;
    el.admin.hidden = true;
    el.reader.hidden = true;
    el.library.hidden = false;
    el.frame.src = 'about:blank';
    document.title = prefs.lang === 'zh' ? '墨阅 · 私人书库' : 'Moyue · Private Library';
    if (updateHistory && location.hash !== '') {
      history.pushState(null, '', location.pathname);
    }
    loadLibrary();
  }

  async function loadLibrary({ silent = false } = {}) {
    try {
      const [libs, books, stats] = await Promise.all([api.libraries(), api.books(), api.stats()]);
      state.libraries = libs.libraries || [];
      state.books = books.books || [];
      state.stats = stats;
      if (state.selectedLibrary !== 'all' && !state.libraries.some((l) => l.id === state.selectedLibrary)) {
        state.selectedLibrary = 'all';
        state.folder = '';
      }
      if (state.folder) {
        const scope = state.selectedLibrary === 'all'
          ? state.books
          : state.books.filter((b) => b.libraryId === state.selectedLibrary);
        const prefix = state.folder + '/';
        const exists = scope.some((b) => {
          const dir = bookFolder(b.file);
          return dir === state.folder || dir.startsWith(prefix);
        });
        if (!exists) state.folder = '';
      }
      renderLibrarySwitch();
      renderStats();
      renderContinue();
      renderShelf();
    } catch (err) {
      if (!silent && err.status !== 401) toast(err.message, { type: 'error' });
    }
  }

  function renderLibrarySwitch() {
    if (state.libraries.length === 0) {
      el.librarySwitch.innerHTML = '';
      return;
    }
    const all = `<button class="lib-chip${state.selectedLibrary === 'all' ? ' is-active' : ''}" data-lib="all">
        全部 <b>${state.books.length}</b></button>`;
    const chips = state.libraries.map((l) => {
      const count = state.books.filter((b) => b.libraryId === l.id).length;
      return `<button class="lib-chip${state.selectedLibrary === l.id ? ' is-active' : ''}" data-lib="${l.id}">
        ${esc(l.name)} <b>${count}</b></button>`;
    }).join('');
    el.librarySwitch.innerHTML = all + chips;
  }

  function renderStats() {
    const st = state.stats || { books: 0, finished: 0, totalSeconds: 0, streak: 0, daily: [] };
    const total = fmtDuration(st.totalSeconds);
    const daily = Array.isArray(st.daily) ? st.daily : [];
    const dots = daily.map((d, i) => {
      const on = d.seconds > 0 ? ' on' : '';
      const today = i === daily.length - 1 ? ' today' : '';
      const label = `${d.date} · ${Math.round(d.seconds / 60)} 分钟`;
      return `<i class="${(on + today).trim()}" title="${esc(label)}"></i>`;
    }).join('');

    el.overview.innerHTML = `
      <div class="stat">
        <span class="stat-label">藏书</span>
        <div class="stat-value">${st.books || 0}<span class="stat-unit">本</span></div>
      </div>
      <div class="stat">
        <span class="stat-label">已读完</span>
        <div class="stat-value">${st.finished || 0}<span class="stat-unit">本</span></div>
      </div>
      <div class="stat">
        <span class="stat-label"><svg class="icon"><use href="#i-clock"/></svg>累计阅读</span>
        <div class="stat-value">${total.value}<span class="stat-unit">${total.unit}</span></div>
      </div>
      <div class="stat">
        <span class="stat-label"><svg class="icon"><use href="#i-fire"/></svg>连续阅读</span>
        <div class="stat-value">${st.streak || 0}<span class="stat-unit">天</span></div>
        <div class="stat-dots">${dots}</div>
      </div>`;
  }

  function libraryBooks() {
    if (state.selectedLibrary === 'all') return state.books;
    return state.books.filter((b) => b.libraryId === state.selectedLibrary);
  }

  // 书库内的相对路径去掉文件名，得到所在文件夹；根目录返回空串。
  function bookFolder(file) {
    const parts = String(file || '').split('/');
    parts.pop();
    return parts.filter(Boolean).join('/');
  }

  // 把当前书库（或当前文件夹）的直接子文件夹和直属书籍分开。
  function folderShelf() {
    const current = state.folder;
    const prefix = current ? current + '/' : '';
    const folders = new Map();
    const direct = [];
    for (const b of libraryBooks()) {
      const path = bookFolder(b.file);
      if (path === current) {
        direct.push(b);
        continue;
      }
      if (current) {
        if (!path.startsWith(prefix)) continue;
        const name = path.slice(prefix.length).split('/')[0];
        if (!name) continue;
        const key = prefix + name;
        const entry = folders.get(key) || { path: key, name, count: 0 };
        entry.count += 1;
        folders.set(key, entry);
      } else {
        const name = path.split('/')[0];
        if (!name) continue;
        const entry = folders.get(name) || { path: name, name, count: 0 };
        entry.count += 1;
        folders.set(name, entry);
      }
    }
    return {
      folders: Array.from(folders.values())
        .sort((a, b) => a.name.localeCompare(b.name, 'zh-Hans-CN')),
      books: direct,
    };
  }

  function openFolder(path) {
    state.folder = path || '';
    state.query = '';
    el.search.value = '';
    closeMenus();
    renderShelf();
    const shelf = el.grid.closest('.shelf');
    if (shelf) shelf.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  function renderFolderTrail() {
    const q = state.query.trim();
    if (!state.folder || q) {
      el.folderTrail.hidden = true;
      el.folderTrail.innerHTML = '';
      return;
    }
    const lib = state.libraries.find((l) => l.id === state.selectedLibrary);
    const rootName = state.selectedLibrary === 'all' ? tr('全部书库') : (lib?.name || tr('书库'));
    const items = [`<button type="button" data-folder-nav="">${esc(rootName)}</button>`];
    let acc = '';
    const parts = state.folder.split('/').filter(Boolean);
    parts.forEach((part, i) => {
      acc = acc ? `${acc}/${part}` : part;
      items.push('<span class="folder-sep">/</span>');
      if (i === parts.length - 1) {
        items.push(`<span class="folder-current">${esc(part)}</span>`);
      } else {
        items.push(`<button type="button" data-folder-nav="${esc(acc)}">${esc(part)}</button>`);
      }
    });
    el.folderTrail.innerHTML = items.join('');
    el.folderTrail.hidden = false;
  }

  function renderContinue() {
    const book = libraryBooks()
      .filter((b) => b.progress && b.progress.updatedAt && !b.progress.finished
        && (b.progress.percent > 0 || b.progress.chapter > 0))
      .sort((a, b) => new Date(b.progress.updatedAt) - new Date(a.progress.updatedAt))[0];
    if (!book) {
      el.continueCard.hidden = true;
      return;
    }
    const p = Math.round(book.progress.percent || 0);
    const chapter = (book.progress.chapter || 0) + 1;
    const unit = book.type === 'audio' ? '段'
      : book.type === 'pdf' || book.type === 'cbz' || book.type === 'cbr' ? '页' : '节';
    el.continueCard.hidden = false;
    el.continueCard.innerHTML = `
      <div class="continue-inner">
        <div class="continue-cover" data-open="${book.id}" role="button" tabindex="0" aria-label="${esc(tr('继续阅读'))} ${esc(book.title)}">
          ${coverMarkup(book)}
        </div>
        <div class="continue-text">
          <span class="eyebrow">${tr('继续阅读')} · ${relativeTime(book.progress.updatedAt)}</span>
          <h2 title="${esc(book.title)}">${esc(book.title)}</h2>
          <p>${esc(book.creator || '佚名')} · 第 ${chapter} ${unit} · 已读 ${p}%</p>
          <div class="continue-bar"><i style="width:${clamp(p, 0, 100)}%"></i></div>
        </div>
        <div class="continue-actions">
          <button class="btn btn-primary" data-open="${book.id}">
            接着读 <svg class="icon"><use href="#i-chevron-right"/></svg>
          </button>
        </div>
      </div>`;
  }

  function coverMarkup(book) {
    const hue = hashHue(book.id);
    const fallback = `<div class="cover-fallback" style="--h:${hue}">
        <span class="cf-title">${esc(book.title)}</span>
        <span class="cf-author">${esc(book.creator || '佚名')}</span>
      </div>`;
    const img = book.hasCover
      ? `<img src="/api/books/${book.id}/cover" alt="" loading="lazy" decoding="async">`
      : '';
    return fallback + img;
  }

  function bookStateText(book) {
    const p = book.progress || {};
    if (p.finished) return '已读完';
    if (p.updatedAt && ((p.percent || 0) > 0 || (p.chapter || 0) > 0)) {
      return `已读 ${Math.round(p.percent || 0)}% · ${relativeTime(p.updatedAt)}`;
    }
    if (p.updatedAt) return '刚开始阅读';
    const kind = typeLabel(book.type);
    return kind ? `${kind} · 尚未开始` : '尚未开始';
  }

  function bookCard(b, i) {
    const p = b.progress || {};
    const percent = clamp(p.percent || 0, 0, 100);
    const badge = p.finished ? '<span class="badge">已读完</span>' : '';
    const typeTag = typeLabel(b.type) ? `<span class="badge badge-type">${typeLabel(b.type)}</span>` : '';
    const bar = p.updatedAt && !p.finished
      ? `<div class="cover-progress"><i style="width:${percent}%"></i></div>` : '';
    const stateCls = p.updatedAt ? '' : ' is-new';
    const lib = state.libraries.find((l) => l.id === b.libraryId);
    const libName = state.libraries.length > 1 ? (lib?.name || '') : '';
    const unit = b.type === 'audio' ? '段'
      : b.type === 'pdf' || b.type === 'cbz' || b.type === 'cbr' ? '页' : '节';
    const pending = lib?.kind === 'webdav' && !b.chapterCount;
    const detail = pending
      ? (b.size > 0 ? `WebDAV · ${fmtBytes(b.size)}` : 'WebDAV')
      : `${b.chapterCount || 0} ${unit}`;
    const bits = [b.creator || '佚名', detail];
    if (libName) bits.push(libName);
    if (state.query.trim()) {
      const dir = bookFolder(b.file);
      if (dir) bits.push(dir);
    }
    return `
      <article class="book" data-id="${b.id}" style="--d:${Math.min(i * 26, 260)}ms">
        <div class="cover" data-open="${b.id}" role="button" tabindex="0" aria-label="打开《${esc(b.title)}》">
          ${coverMarkup(b)}
          ${bar}
          ${badge}${typeTag}
          <button class="card-menu" data-menu="${b.id}" aria-label="更多操作" aria-haspopup="true">
            <svg class="icon"><use href="#i-more"/></svg>
          </button>
        </div>
        <div class="book-menu" data-menu-panel="${b.id}">
          <button data-act="open" data-id="${b.id}"><svg class="icon"><use href="#i-book"/></svg>开始阅读</button>
          <button data-act="reset" data-id="${b.id}"><svg class="icon"><use href="#i-rotate"/></svg>标记未读</button>
          <button data-act="download" data-id="${b.id}"><svg class="icon"><use href="#i-download"/></svg>下载文件</button>
          ${state.user && state.user.isAdmin ? `<button data-act="delete" data-id="${b.id}" class="is-danger"><svg class="icon"><use href="#i-trash"/></svg>删除</button>` : ''}
        </div>
        <div class="book-meta">
          <h3 title="${esc(b.title)}">${esc(b.title)}</h3>
          <p>${esc(bits.join(' · '))}</p>
          <div class="book-state${stateCls}">${esc(bookStateText(b))}</div>
        </div>
      </article>`;
  }

  function countBooks(n) {
    if (prefs.lang === 'en') return `${n} book${n === 1 ? '' : 's'}`;
    return tr(`${n} 本`);
  }

  function countFound(n) {
    if (prefs.lang === 'en') return `Found ${n} book${n === 1 ? '' : 's'}`;
    return tr(`找到 ${n} 本`);
  }

  function folderCard(f, i) {
    const count = countBooks(f.count);
    return `
      <article class="book folder-card" data-folder="${esc(f.path)}" style="--d:${Math.min(i * 26, 260)}ms">
        <div class="cover folder-cover" role="button" tabindex="0" aria-label="${esc(tr('文件夹'))} ${esc(f.name)}">
          <svg class="icon folder-icon"><use href="#i-folder"/></svg>
          <span class="folder-count">${esc(count)}</span>
        </div>
        <div class="book-meta">
          <h3 title="${esc(f.name)}">${esc(f.name)}</h3>
          <p>${esc(tr('文件夹'))} · ${esc(count)}</p>
        </div>
      </article>`;
  }

  function renderShelf() {
    const q = state.query.trim().toLowerCase();
    const all = libraryBooks();
    let folders = [];
    let books;
    if (q) {
      books = all.filter((b) =>
        (b.title || '').toLowerCase().includes(q) || (b.creator || '').toLowerCase().includes(q));
    } else {
      const shelf = folderShelf();
      folders = shelf.folders;
      books = shelf.books;
    }

    const sorters = {
      recent: (a, b) => new Date(b.progress?.updatedAt || b.addedAt) - new Date(a.progress?.updatedAt || a.addedAt),
      added: (a, b) => new Date(b.addedAt) - new Date(a.addedAt),
      title: (a, b) => (a.title || '').localeCompare(b.title || '', 'zh-Hans-CN'),
    };
    books.sort(sorters[state.sort] || sorters.recent);

    const total = folders.reduce((n, f) => n + f.count, 0) + books.length;
    el.shelfCount.textContent = q ? countFound(books.length) : countBooks(total);
    el.shelfTitle.textContent = state.folder
      ? state.folder.split('/').filter(Boolean).pop()
      : tr('我的书架');
    renderFolderTrail();

    const itemCount = folders.length + books.length;
    el.grid.hidden = itemCount === 0;
    el.empty.hidden = itemCount !== 0;

    if (itemCount === 0) {
      const heading = el.empty.querySelector('h3');
      const desc = el.empty.querySelector('p');
      const inFolder = !!state.folder;
      const canUpload = !!(state.user && state.user.isAdmin);
      el.emptyUpload.hidden = inFolder || !canUpload || state.libraries.length === 0;
      el.emptyHint.hidden = inFolder || !canUpload || state.libraries.length === 0;
      if (inFolder) {
        heading.textContent = tr('这个文件夹是空的');
        desc.textContent = tr('返回上一级继续浏览。');
      } else if (state.books.length === 0) {
        if (state.user && state.user.isAdmin && state.libraries.length > 0) {
          heading.textContent = tr('书架还空着');
          desc.textContent = tr('把 EPUB 或 TXT 文件拖进窗口，或者点击下面按钮开始。');
        } else if (state.user && state.user.isAdmin) {
          heading.textContent = tr('还没有书库');
          desc.textContent = tr('先到管理控制台添加一个服务器目录，再导入书籍。');
        } else {
          heading.textContent = tr('等待管理员分配书籍');
          desc.textContent = tr('你的账号还没有可访问的书库，请联系管理员授权。');
        }
      } else if (q) {
        heading.textContent = tr('没有匹配的书');
        desc.textContent = tr('换一个关键词试试，或者清空搜索框。');
      } else {
        heading.textContent = tr('这个书库还没有书');
        desc.textContent = tr('切换其他书库，或让管理员扫描目录。');
      }
    }

    el.grid.innerHTML =
      folders.map((f, i) => folderCard(f, i)).join('') +
      books.map((b, i) => bookCard(b, folders.length + i)).join('');
  }

  /* ============================ 管理台 ============================ */

  function openAdmin({ updateHistory = true } = {}) {
    if (!state.user || !state.user.isAdmin) {
      toast('需要管理员权限', { type: 'error' });
      enterLibrary();
      return;
    }
    stopHeartbeat();
    if (state.tts.active) stopTTS();
    state.view = 'admin';
    document.body.classList.remove('reading');
    el.authView.hidden = true;
    el.library.hidden = true;
    el.reader.hidden = true;
    el.admin.hidden = false;
    el.frame.src = 'about:blank';
    state.current = null;
    document.title = prefs.lang === 'zh' ? '管理控制台 · 墨阅' : 'Admin console · Moyue';
    if (updateHistory && location.hash !== '#/admin') {
      history.pushState(null, '', '#/admin');
    }
    loadAdmin();
  }

  async function loadAdmin() {
    try {
      const [libs, users] = await Promise.all([api.adminLibraries(), api.adminUsers()]);
      state.admin.libraries = libs.libraries || [];
      state.admin.users = users.users || [];
      renderLibraryChecks();
      renderAdminLibraries();
      renderAdminUsers();
      scheduleAdminPoll();
      try {
        const data = await api.adminTTS();
        state.admin.tts = data.tts;
        renderTTSSettings();
      } catch (err) {
        if (err.status !== 401) {
          el.ttsKeyState.className = 'tts-key-state is-bad';
          el.ttsKeyState.textContent = err.message;
        }
      }
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    }
  }

  function scheduleAdminPoll() {
    const running = state.admin.libraries.some((l) => l.scan && l.scan.running);
    if (running && !state.admin.poll) {
      state.admin.poll = setInterval(async () => {
        if (state.view !== 'admin') return;
        try {
          const libs = await api.adminLibraries();
          state.admin.libraries = libs.libraries || [];
          renderAdminLibraries();
          if (!state.admin.libraries.some((l) => l.scan && l.scan.running)) {
            stopAdminPoll();
            loadLibrary({ silent: true });
          }
        } catch { /* 忽略 */ }
      }, 2000);
    } else if (!running) {
      stopAdminPoll();
    }
  }

  function stopAdminPoll() {
    clearInterval(state.admin.poll);
    state.admin.poll = 0;
  }

  function scanText(scan) {
    if (!scan) return tr('尚未扫描');
    if (scan.running) {
      return `<span class="spinner-sm"></span>${esc(tr(`正在扫描… 已入库 ${scan.added || 0} 本`))}`;
    }
    const parts = [];
    if (scan.lastScan) parts.push(`${tr('上次扫描')} ${tr(relativeTime(scan.lastScan))}`);
    parts.push(tr(`新增 ${scan.added || 0} · 更新 ${scan.updated || 0} · 跳过 ${scan.skipped || 0}`));
    if (scan.removed) parts.push(tr(`移除 ${scan.removed}`));
    if (scan.failed) parts.push(tr(`失败 ${scan.failed}`));
    return esc(parts.join(' · '));
  }

  function libTypeLabel(type) {
    const label = {
      txt: 'TXT', epub: 'EPUB', ebook: '电子书', comic: '漫画', pdf: 'PDF',
      kindle: 'Kindle', audio: '有声书', mixed: '全部格式',
    }[type] || type;
    return tr(label);
  }

  function renderAdminLibraries() {
    el.libList.innerHTML = state.admin.libraries.map((l) => {
      const dav = l.kind === 'webdav';
      return `
      <div class="admin-item" data-lib-row="${l.id}">
        <div class="admin-item-main">
          <div class="admin-item-title">
            ${esc(l.name)}
            ${dav ? '<span class="tag">WebDAV</span>' : ''}
            <span class="tag">${libTypeLabel(l.type)}</span>
            <span class="tag tag-accent">${l.bookCount || 0} 本</span>
            ${l.scan && l.scan.error ? '<span class="tag tag-bad">扫描有错误</span>' : ''}
          </div>
          <div class="admin-item-sub">${esc(dav ? (l.url || '') : (l.path || ''))}</div>
          <div class="scan-line" title="${esc(l.scan?.error || '')}">${scanText(l.scan)}</div>
        </div>
        <div class="admin-item-actions">
          <button class="btn btn-ghost btn-sm" data-lib-act="scan" data-id="${l.id}" ${l.scan?.running ? 'disabled' : ''}>
            <svg class="icon"><use href="#i-scan"/></svg>扫描
          </button>
          <button class="btn btn-ghost btn-sm" data-lib-act="delete" data-id="${l.id}">删除</button>
        </div>
      </div>`;
    }).join('') || '<div class="empty-list">还没有书库，先在上方添加一个服务器目录。</div>';
  }

  function renderLibraryChecks() {
    const libs = state.admin.libraries;
    if (libs.length === 0) {
      el.userLibChecks.innerHTML = '<span class="panel-desc">还没有书库，创建后即可分配权限。</span>';
      return;
    }
    el.userLibChecks.innerHTML = libs.map((l) => `
      <label><input type="checkbox" name="lib" value="${l.id}">${esc(l.name)}</label>
    `).join('');
  }

  function renderAdminUsers() {
    const libName = (id) => state.admin.libraries.find((l) => l.id === id)?.name || '未知书库';
    el.userList.innerHTML = state.admin.users.map((u) => {
      const libs = u.isAdmin
        ? '<span class="tag tag-accent">全部书库</span>'
        : (u.libraries || []).map((id) => `<span class="tag">${esc(libName(id))}</span>`).join('') || '<span class="tag">未分配</span>';
      const self = state.user && state.user.id === u.id;
      return `<div class="admin-item">
        <div class="admin-item-main">
          <div class="admin-item-title">
            ${esc(u.displayName || u.username)}
            <span class="admin-item-sub" style="margin:0">@${esc(u.username)}</span>
            ${u.isAdmin ? '<span class="tag tag-accent">管理员</span>' : ''}
            ${self ? '<span class="tag">当前账号</span>' : ''}
          </div>
          <div class="admin-item-sub">${libs}</div>
        </div>
        <div class="admin-item-actions">
          <button class="btn btn-ghost btn-sm" data-user-act="edit" data-id="${u.id}">编辑</button>
          ${self ? '' : `<button class="btn btn-ghost btn-sm" data-user-act="delete" data-id="${u.id}">删除</button>`}
        </div>
      </div>`;
    }).join('') || '<div class="empty-list">还没有其他账号。</div>';
  }

  function startUserEdit(id) {
    const u = state.admin.users.find((x) => x.id === id);
    if (!u) return;
    state.admin.editUserId = id;
    el.userFormTitle.textContent = '编辑用户';
    el.userFormDesc.textContent = `修改 ${u.displayName || u.username} 的密码、角色与书库权限。`;
    el.userForm.username.value = u.username;
    el.userForm.username.disabled = true;
    el.userForm.displayName.value = u.displayName || '';
    el.userForm.password.value = '';
    el.userForm.password.placeholder = '留空则不修改';
    el.userForm.isAdmin.checked = !!u.isAdmin;
    $$('input[name="lib"]', el.userLibChecks).forEach((cb) => {
      cb.checked = (u.libraries || []).includes(cb.value);
    });
    el.userSubmit.textContent = '保存修改';
    el.userCancelEdit.hidden = false;
    el.userForm.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }

  function resetUserForm() {
    state.admin.editUserId = null;
    el.userForm.reset();
    el.userForm.username.disabled = false;
    el.userForm.password.placeholder = '至少 6 位';
    el.userFormTitle.textContent = '创建用户';
    el.userFormDesc.textContent = '为家人或朋友创建独立账号，每个人的阅读进度互不影响。';
    el.userSubmit.textContent = '创建用户';
    el.userCancelEdit.hidden = true;
  }

  async function submitUserForm(event) {
    event.preventDefault();
    const fd = new FormData(el.userForm);
    const libs = $$('input[name="lib"]:checked', el.userLibChecks).map((cb) => cb.value);
    const password = String(fd.get('password') || '');
    const isEdit = !!state.admin.editUserId;

    el.userSubmit.disabled = true;
    try {
      if (isEdit) {
        const payload = {
          displayName: String(fd.get('displayName') || ''),
          isAdmin: fd.get('isAdmin') === 'on',
          libraries: libs,
        };
        if (password) payload.password = password;
        await api.updateUser(state.admin.editUserId, payload);
        if (state.user && state.user.id === state.admin.editUserId && payload.displayName) {
          state.user.displayName = payload.displayName;
          setUser({ ...state.user });
        }
        toast('已保存用户信息');
      } else {
        await api.createUser({
          username: String(fd.get('username') || '').trim(),
          displayName: String(fd.get('displayName') || '').trim(),
          password,
          isAdmin: fd.get('isAdmin') === 'on',
          libraries: libs,
        });
        toast('已创建用户');
      }
      resetUserForm();
      await loadAdmin();
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    } finally {
      el.userSubmit.disabled = false;
    }
  }

  /* ---------------------------- 管理台 · 语音朗读设置 ---------------------------- */

  const TTS_DEFAULTS = {
    mimo: { url: 'https://api.xiaomimimo.com/v1/chat/completions', model: 'mimo-v2.5-tts', voice: 'mimo_default' },
    openai: { url: 'https://api.openai.com/v1/audio/speech', model: 'tts-1', voice: 'alloy' },
  };

  function fillAdminVoices(provider, selected) {
    const list = provider === 'openai' ? OPENAI_VOICES : MIMO_VOICES;
    el.ttsAdminVoice.innerHTML = list.map(([id, name]) =>
      `<option value="${esc(id)}">${esc(name)}（${esc(id)}）</option>`).join('');
    if (selected && list.some(([id]) => id === selected)) el.ttsAdminVoice.value = selected;
    else el.ttsAdminVoice.value = TTS_DEFAULTS[provider]?.voice || list[0][0];
  }

  function renderTTSSettings() {
    const t = state.admin.tts;
    if (!t) return;
    state.admin.ttsClearKey = false;
    el.ttsForm.enabled.checked = !!t.enabled;
    el.ttsProvider.value = t.provider || 'mimo';
    el.ttsBaseUrl.value = t.baseUrl || TTS_DEFAULTS[t.provider || 'mimo'].url;
    el.ttsModel.value = t.model || TTS_DEFAULTS[t.provider || 'mimo'].model;
    fillAdminVoices(t.provider || 'mimo', t.voice);
    el.ttsApiKey.value = '';
    el.ttsApiKey.placeholder = t.hasKey ? `已保存 ${t.keyHint}，留空则不修改` : '粘贴 API Key';
    el.ttsKeyState.className = 'tts-key-state' + (t.hasKey ? ' is-ok' : '');
    el.ttsKeyState.textContent = t.hasKey
      ? `已配置 API Key（${t.keyHint}），密钥只保存在服务器。`
      : '尚未保存 API Key，启用前需要先填写。';
    el.ttsClearKey.hidden = !t.hasKey;
    const ready = !!t.enabled && !!t.hasKey;
    el.ttsStateTag.textContent = ready ? '已启用' : t.hasKey ? '已配置 · 未启用' : '未配置';
    el.ttsStateTag.className = 'tag' + (ready ? ' tag-accent' : '');
    el.ttsTestResult.hidden = true;
    el.ttsTestResult.innerHTML = '';
  }

  function ttsFormPayload() {
    const fd = new FormData(el.ttsForm);
    return {
      enabled: fd.get('enabled') === 'on',
      provider: String(fd.get('provider') || 'mimo'),
      baseUrl: String(fd.get('baseUrl') || '').trim(),
      model: String(fd.get('model') || '').trim(),
      voice: String(fd.get('voice') || '').trim(),
      apiKey: el.ttsApiKey.value.trim(),
      clearKey: !!state.admin.ttsClearKey,
    };
  }

  el.ttsProvider.addEventListener('change', () => {
    const provider = el.ttsProvider.value === 'openai' ? 'openai' : 'mimo';
    const def = TTS_DEFAULTS[provider];
    const knownURLs = Object.values(TTS_DEFAULTS).map((d) => d.url);
    const knownModels = Object.values(TTS_DEFAULTS).map((d) => d.model);
    if (!el.ttsBaseUrl.value.trim() || knownURLs.includes(el.ttsBaseUrl.value.trim())) {
      el.ttsBaseUrl.value = def.url;
    }
    if (!el.ttsModel.value.trim() || knownModels.includes(el.ttsModel.value.trim())) {
      el.ttsModel.value = def.model;
    }
    fillAdminVoices(provider, '');
  });

  el.ttsClearKey.addEventListener('click', () => {
    state.admin.ttsClearKey = true;
    el.ttsApiKey.value = '';
    el.ttsApiKey.placeholder = '保存后将清除服务器上的密钥';
    el.ttsKeyState.className = 'tts-key-state is-bad';
    el.ttsKeyState.textContent = '保存设置后将清除已配置的 API Key。';
    el.ttsClearKey.hidden = true;
  });

  el.ttsForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const payload = ttsFormPayload();
    if (payload.enabled && !payload.apiKey && !state.admin.tts?.hasKey && !payload.clearKey) {
      toast('启用语音朗读前请填写 API Key', { type: 'error' });
      return;
    }
    el.ttsSave.disabled = true;
    try {
      const data = await api.saveAdminTTS(payload);
      state.admin.tts = data.tts;
      renderTTSSettings();
      toast(payload.enabled ? '语音朗读已保存并启用' : '语音朗读设置已保存');
      await loadTTSStatus();
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    } finally {
      el.ttsSave.disabled = false;
    }
  });

  el.ttsTest.addEventListener('click', async () => {
    const payload = ttsFormPayload();
    if (!payload.apiKey && !state.admin.tts?.hasKey) {
      toast('请先填写 API Key', { type: 'error' });
      return;
    }
    el.ttsTest.disabled = true;
    el.ttsTestResult.hidden = false;
    el.ttsTestResult.innerHTML = '<span class="spinner-sm"></span> 正在合成测试语音…';
    try {
      const res = await fetch('/api/admin/settings/tts/test', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!res.ok) {
        let msg = `测试失败（${res.status}）`;
        try {
          const data = await res.json();
          if (data.error) msg = data.error;
        } catch { /* 忽略 */ }
        throw new Error(msg);
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      el.ttsTestResult.innerHTML = '<strong>测试成功</strong> · 正在播放示例语音。';
      const audio = document.createElement('audio');
      audio.controls = true;
      audio.src = url;
      el.ttsTestResult.appendChild(audio);
      audio.play().catch(() => { /* 用户可手动点击播放 */ });
      audio.addEventListener('ended', () => URL.revokeObjectURL(url), { once: true });
      toast('语音合成连接正常');
    } catch (err) {
      el.ttsTestResult.innerHTML = `<strong>测试失败</strong><br>${esc(err.message)}`;
    } finally {
      el.ttsTest.disabled = false;
    }
  });

  /* ============================ 阅读器 ============================ */

  function closeMenus() {
    $$('.book-menu.open').forEach((m) => m.classList.remove('open'));
    $$('.book.menu-open').forEach((b) => b.classList.remove('menu-open'));
  }

  function closeDrawers() {
    el.tocDrawer.classList.remove('is-open');
    el.settingsDrawer.classList.remove('is-open');
    el.scrim.classList.remove('is-visible');
  }

  function drawerOpen() {
    return el.tocDrawer.classList.contains('is-open') || el.settingsDrawer.classList.contains('is-open');
  }

  function openDrawer(which, tab) {
    hideAnnoBar();
    const target = which === 'toc' ? el.tocDrawer : el.settingsDrawer;
    const other = which === 'toc' ? el.settingsDrawer : el.tocDrawer;
    other.classList.remove('is-open');
    if (tab) switchTab(tab);
    target.classList.add('is-open');
    el.scrim.classList.add('is-visible');
  }

  function switchTab(name) {
    $$('.tab', el.tocDrawer).forEach((t) => t.classList.toggle('is-active', t.dataset.tab === name));
    el.tocList.hidden = name !== 'toc';
    el.markList.hidden = name !== 'marks';
    el.noteList.hidden = name !== 'notes';
    el.searchPane.hidden = name !== 'search';
    hideAnnoBar();
    if (name === 'marks') renderBookmarks();
    if (name === 'notes') renderNotes();
    if (name === 'search') {
      setTimeout(() => el.searchInputInBook.focus(), 60);
      if (!state.searchDone) {
        el.searchMeta.textContent = searchSupported() ? '' : '该格式暂不支持全文搜索';
      }
    }
  }

  function searchSupported() {
    const t = state.current?.type;
    return ['txt', 'epub', 'fb2', 'mobi', 'azw', 'azw3', 'prc'].includes(t);
  }

  function renderTOC() {
    const book = state.current;
    if (!book) return;
    el.tocList.innerHTML = book.toc.map((item) => {
      const active = item.chapter === state.chapter ? ' is-active' : '';
      const indent = Math.min(item.level || 0, 3) * 14;
      const disabled = item.chapter < 0 ? ' disabled' : '';
      return `<li class="toc-item${active}">
        <button data-chapter="${item.chapter}" style="padding-left:${12 + indent}px"${disabled}>
          <span class="toc-index">${item.chapter >= 0 ? item.chapter + 1 : '·'}</span>
          <span>${esc(item.title)}</span>
        </button>
      </li>`;
    }).join('');
  }

  function updateTOCActive() {
    $$('.toc-item', el.tocList).forEach((li, i) => {
      const item = state.current?.toc?.[i];
      li.classList.toggle('is-active', !!item && item.chapter === state.chapter);
    });
  }

  function renderBookmarks() {
    const book = state.current;
    if (!book) return;
    const marks = book.bookmarks || [];
    el.markCount.textContent = marks.length;
    if (marks.length === 0) {
      el.markList.innerHTML = `<div class="blank">
        <svg class="icon"><use href="#i-bookmark"/></svg>
        还没有书签。<br>阅读时点击顶部书签图标即可记录当前位置。
      </div>`;
      return;
    }
    el.markList.innerHTML = marks.map((m) => `
      <div class="mark" data-jump="${m.chapter}" data-fraction="${m.percent}">
        <div class="mark-head">
          <span class="mark-chapter">${esc(m.chapterTitle || `第 ${m.chapter + 1} 节`)}</span>
          <span class="mark-time">${esc(relativeTime(m.createdAt))}</span>
        </div>
        <p class="mark-snippet">${esc(m.snippet || '（此处无文字摘录）')}</p>
        <button class="mark-del" data-del="${m.id}" aria-label="删除书签">
          <svg class="icon"><use href="#i-trash"/></svg>
        </button>
      </div>`).join('');
  }

  function renderNotes() {
    const book = state.current;
    if (!book) return;
    const list = (book.annotations || []).slice().sort((a, b) =>
      a.chapter - b.chapter || a.start - b.start);
    el.noteCount.textContent = String(list.length);
    if (list.length === 0) {
      el.noteList.innerHTML = `<div class="blank">
        <svg class="icon"><use href="#i-bookmark"/></svg>
        还没有划线或笔记。<br>选中正文文字即可划线，或添加一条笔记。
      </div>`;
      return;
    }
    el.noteList.innerHTML = list.map((a) => {
      const title = book.chapters[a.chapter]?.title || `第 ${a.chapter + 1} 节`;
      return `<div class="note-item" data-note="${a.id}" data-color="${esc(a.color || 'yellow')}">
        <div class="note-head">
          <span class="note-chapter">${esc(title)}</span>
          <span class="note-time">${esc(relativeTime(a.updatedAt || a.createdAt))}</span>
        </div>
        <p class="note-quote-text">${esc(a.text || '')}</p>
        ${a.note ? `<p class="note-body">${esc(a.note)}</p>` : ''}
        <div class="note-actions-bar">
          <button class="note-mini" data-note-edit="${a.id}">编辑</button>
          <button class="note-mini" data-note-del="${a.id}">删除</button>
        </div>
      </div>`;
    }).join('');
  }

  function annotationsForChapter() {
    const book = state.current;
    if (!book) return [];
    return (book.annotations || []).filter((a) => a.chapter === state.chapter);
  }

  function clearHighlights(doc) {
    doc.querySelectorAll('mark.moyue-hl').forEach((mark) => {
      const parent = mark.parentNode;
      if (!parent) return;
      while (mark.firstChild) parent.insertBefore(mark.firstChild, mark);
      parent.removeChild(mark);
      parent.normalize();
    });
  }

  function textNodes(doc) {
    const walker = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        if (!node.nodeValue) return NodeFilter.FILTER_REJECT;
        const parent = node.parentElement;
        if (!parent) return NodeFilter.FILTER_REJECT;
        if (parent.closest('script,style,noscript,svg')) return NodeFilter.FILTER_REJECT;
        return NodeFilter.FILTER_ACCEPT;
      },
    });
    const nodes = [];
    let offset = 0;
    while (walker.nextNode()) {
      const node = walker.currentNode;
      const len = node.nodeValue.length;
      nodes.push({ node, start: offset, end: offset + len });
      offset += len;
    }
    return nodes;
  }

  function applyAnnotationsForChapter() {
    const book = state.current;
    if (!book || isFixed(book) || isAudio(book)) return;
    const doc = el.frame.contentDocument;
    if (!doc || !doc.body) return;
    clearHighlights(doc);
    for (const a of annotationsForChapter()) {
      if (a.end <= a.start) continue;
      for (const tn of textNodes(doc)) {
        if (tn.end <= a.start || tn.start >= a.end) continue;
        const from = Math.max(0, a.start - tn.start);
        const to = Math.min(tn.node.nodeValue.length, a.end - tn.start);
        if (to <= from) continue;
        try {
          const range = doc.createRange();
          range.setStart(tn.node, from);
          range.setEnd(tn.node, to);
          const mark = doc.createElement('mark');
          mark.className = 'moyue-hl';
          mark.dataset.id = a.id;
          mark.dataset.color = a.color || 'yellow';
          range.surroundContents(mark);
        } catch { /* 跨节点或 DOM 变动时跳过 */ }
      }
    }
  }

  function revealInPage(node) {
    if (!node) return false;
    if (isPaginated() && state.page.active) {
      const rect = node.getBoundingClientRect();
      const x = rect.left + state.page.page * state.page.vw;
      const page = clamp(Math.floor((x + 6) / state.page.vw), 0, state.page.pages - 1);
      if (page !== state.page.page) setPage(page);
      return true;
    }
    node.scrollIntoView({ block: 'center', behavior: 'smooth' });
    return true;
  }

  function focusAnnotation(id, anchor) {
    const doc = el.frame.contentDocument;
    if (!doc) return;
    const mark = doc.querySelector(`mark.moyue-hl[data-id="${id}"]`);
    if (!mark) {
      if (anchor) {
        if (isPaginated() && state.page.active) {
          setPage(Math.round(clamp(anchor.percent || 0, 0, 1) * (state.page.pages - 1)));
        } else {
          const win = el.frame.contentWindow;
          const max = Math.max(0, (doc.scrollingElement || doc.documentElement).scrollHeight - win.innerHeight);
          win.scrollTo(0, max * clamp(anchor.percent || 0, 0, 1));
        }
      }
      return;
    }
    revealInPage(mark);
    mark.classList.add('is-focus');
    setTimeout(() => mark.classList.remove('is-focus'), 1600);
  }

  function hideAnnoBar() {
    if (!el.annoBar) return;
    el.annoBar.classList.remove('is-visible');
    el.annoBar.hidden = true;
    state.selection = null;
  }

  function showAnnoBar(selection) {
    state.selection = selection;
    el.annoBar.hidden = false;
    requestAnimationFrame(() => el.annoBar.classList.add('is-visible'));
  }

  function readSelection() {
    const book = state.current;
    if (!book || isFixed(book) || isAudio(book)) return null;
    const doc = el.frame.contentDocument;
    const win = el.frame.contentWindow;
    if (!doc || !win) return null;
    const sel = win.getSelection();
    if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return null;
    const range = sel.getRangeAt(0);
    if (!doc.body.contains(range.commonAncestorContainer)) return null;
    const text = sel.toString();
    if (!text.trim()) return null;
    if (text.length > 2000) {
      toast('选择内容过长，请分段划线', { type: 'error' });
      return null;
    }
    const pre = doc.createRange();
    pre.selectNodeContents(doc.body);
    pre.setEnd(range.startContainer, range.startOffset);
    const start = pre.toString().length;
    const mid = doc.createRange();
    mid.selectNodeContents(doc.body);
    mid.setEnd(range.endContainer, range.endOffset);
    const end = mid.toString().length;
    if (end <= start) return null;
    return { chapter: state.chapter, start, end, text };
  }

  function handleSelection() {
    setTimeout(() => {
      if (state.view !== 'reader') return;
      const selection = readSelection();
      if (selection) showAnnoBar(selection);
      else hideAnnoBar();
    }, 20);
  }

  function clearFrameSelection() {
    try {
      el.frame.contentWindow.getSelection()?.removeAllRanges();
    } catch { /* 忽略 */ }
  }

  async function saveHighlight(color) {
    const book = state.current;
    const selection = state.selection;
    if (!book || !selection) return;
    try {
      const res = await api.addAnnotation(book.id, {
        chapter: selection.chapter,
        start: selection.start,
        end: selection.end,
        text: selection.text,
        note: '',
        color,
      });
      book.annotations = [...(book.annotations || []), res.annotation];
      applyAnnotationsForChapter();
      renderNotes();
      hideAnnoBar();
      clearFrameSelection();
      toast('已添加划线');
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    }
  }

  function openNoteDialog(annotation = null) {
    const book = state.current;
    if (!book) return;
    state.noteEditing = annotation;
    state.noteSelection = annotation ? null : state.selection;
    if (!annotation && !state.noteSelection) return;
    const text = annotation ? annotation.text : state.noteSelection.text;
    state.noteColor = annotation ? annotation.color || 'yellow' : 'yellow';
    el.noteTitle.textContent = annotation ? '编辑笔记' : '添加笔记';
    el.noteQuote.textContent = text;
    el.noteText.value = annotation ? annotation.note || '' : '';
    el.noteDelete.hidden = !annotation;
    el.noteColors.querySelectorAll('.anno-color').forEach((c) => {
      c.classList.toggle('is-active', c.dataset.color === state.noteColor);
    });
    el.noteScrim.hidden = false;
    setTimeout(() => el.noteText.focus(), 80);
  }

  function closeNoteDialog() {
    el.noteScrim.hidden = true;
    state.noteEditing = null;
    state.noteSelection = null;
    el.noteText.value = '';
  }

  async function saveNote() {
    const book = state.current;
    if (!book) return;
    const note = el.noteText.value.trim();
    const color = state.noteColor || 'yellow';
    try {
      if (state.noteEditing) {
        const res = await api.updateAnnotation(book.id, state.noteEditing.id, { note, color });
        const idx = (book.annotations || []).findIndex((a) => a.id === state.noteEditing.id);
        if (idx >= 0) book.annotations[idx] = res.annotation;
      } else if (state.noteSelection) {
        const sel = state.noteSelection;
        const res = await api.addAnnotation(book.id, {
          chapter: sel.chapter,
          start: sel.start,
          end: sel.end,
          text: sel.text,
          note,
          color,
        });
        book.annotations = [...(book.annotations || []), res.annotation];
      }
      closeNoteDialog();
      applyAnnotationsForChapter();
      renderNotes();
      hideAnnoBar();
      clearFrameSelection();
      toast('笔记已保存');
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    }
  }

  async function deleteAnnotation(id) {
    const book = state.current;
    if (!book || !id) return;
    try {
      await api.removeAnnotation(book.id, id);
      book.annotations = (book.annotations || []).filter((a) => a.id !== id);
      closeNoteDialog();
      applyAnnotationsForChapter();
      renderNotes();
      hideAnnoBar();
      toast('已删除笔记');
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    }
  }

  function jumpToAnnotation(annotation) {
    if (!annotation) return;
    closeDrawers();
    hideAnnoBar();
    state.pendingAnnotation = annotation.id;
    if (annotation.chapter === state.chapter) {
      setTimeout(() => focusAnnotation(annotation.id, annotation), 60);
      return;
    }
    loadChapter(annotation.chapter, 0);
  }

  function revealRect(rect) {
    if (!rect) return;
    const win = el.frame.contentWindow;
    if (!win) return;
    if (isPaginated() && state.page.active) {
      const x = rect.left + state.page.page * state.page.vw;
      const page = clamp(Math.floor((x + 6) / state.page.vw), 0, state.page.pages - 1);
      if (page !== state.page.page) setPage(page);
      return;
    }
    if (rect.top >= 0 && rect.bottom <= (win.innerHeight || 0)) return;
    const doc = el.frame.contentDocument;
    const scroller = doc?.scrollingElement || doc?.documentElement;
    const currentTop = win.scrollY || scroller?.scrollTop || 0;
    const targetTop = rect.top + currentTop - (win.innerHeight || 0) * 0.3;
    win.scrollTo({ top: Math.max(0, targetTop), behavior: 'smooth' });
  }

  function scrollToQuery(query, fraction = 0) {
    const doc = el.frame.contentDocument;
    const win = el.frame.contentWindow;
    if (!doc || !win || !query) return;
    const needle = query.toLowerCase();
    const nodes = textNodes(doc);
    if (!nodes.length) return;
    const total = nodes[nodes.length - 1].end;
    const wanted = clamp(Number(fraction) || 0, 0, 1) * total;
    let best = null;
    for (const tn of nodes) {
      const value = (tn.node.nodeValue || '').toLowerCase();
      let from = 0;
      while (from <= value.length) {
        const idx = value.indexOf(needle, from);
        if (idx < 0) break;
        const absolute = tn.start + idx;
        const dist = Math.abs(absolute - wanted);
        if (!best || dist < best.dist) best = { tn, idx, dist };
        from = idx + Math.max(1, needle.length);
      }
    }
    if (!best) return;
    const range = doc.createRange();
    range.setStart(best.tn.node, best.idx);
    range.setEnd(best.tn.node, Math.min(best.tn.node.nodeValue.length, best.idx + query.length));
    revealRect(range.getBoundingClientRect());
  }

  function jumpToSearchHit(hit) {
    const book = state.current;
    if (!book) return;
    const chapter = Number(hit.dataset.chapter);
    const percent = clamp(Number(hit.dataset.percent) || 0, 0, 1);
    const q = hit.dataset.query || '';
    state.pendingSearch = { q, chapter, percent };
    if (chapter === state.chapter && isTextBook(book) && el.frame.contentDocument?.body) {
      state.pendingSearch = null;
      state.fraction = percent;
      state.pendingFraction = percent;
      if (prefs.mode === 'scroll') {
        restoreScroll(percent);
        requestAnimationFrame(() => restoreScroll(percent));
      } else {
        paginateText(percent);
      }
      updateChrome();
      updateProgressUI();
      scheduleSave();
      setTimeout(() => scrollToQuery(q, percent), 80);
      return;
    }
    loadChapter(chapter, percent);
  }

  async function runSearch() {
    const book = state.current;
    if (!book) return;
    const q = el.searchInputInBook.value.trim();
    if (q.length < 1) {
      el.searchMeta.textContent = '请输入搜索词';
      return;
    }
    if (!searchSupported()) {
      el.searchMeta.textContent = '该格式暂不支持全文搜索';
      el.searchResults.innerHTML = '';
      return;
    }
    el.searchMeta.textContent = '搜索中…';
    el.searchResults.innerHTML = '';
    const seq = ++state.searchSeq;
    try {
      const data = await api.search(book.id, q);
      if (seq !== state.searchSeq || state.current?.id !== book.id) return;
      state.searchDone = true;
      const results = data.results || [];
      if (results.length === 0) {
        el.searchMeta.textContent = '没有找到匹配内容';
        return;
      }
      el.searchMeta.textContent = `找到 ${results.length} 处${data.truncated ? '（仅显示前 80 处）' : ''}`;
      const re = new RegExp(q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'gi');
      el.searchResults.innerHTML = results.map((hit) => {
        const snippet = esc(hit.snippet || '').replace(re, (m) => `<mark>${m}</mark>`);
        return `<div class="search-hit" data-chapter="${hit.chapter}" data-percent="${hit.percent}" data-query="${esc(q)}">
          <div class="search-hit-head">
            <span class="search-hit-title">${esc(hit.chapterTitle || `第 ${hit.chapter + 1} 节`)}</span>
            <span>${Math.round((hit.percent || 0) * 100)}%</span>
          </div>
          <p>${snippet}</p>
        </div>`;
      }).join('');
    } catch (err) {
      el.searchMeta.textContent = err.message;
    }
  }

  function brandName() {
    return prefs.lang === 'zh' ? '墨阅' : 'Moyue';
  }

  function applyLang() {
    prefs.lang = normalizeLang(prefs.lang);
    const lang = prefs.lang;
    document.documentElement.lang = lang === 'zh' ? 'zh-CN' : lang;
    if (window.I18N) window.I18N.setLang(lang);
    syncLangUI();
    if (state.view === 'reader' && state.current) {
      document.title = `${state.current.title} · ${lang === 'zh' ? '墨阅' : 'Moyue'}`;
    } else if (state.view === 'library' || state.view === 'admin') {
      document.title = lang === 'zh' ? '墨阅 · 私人书库' : 'Moyue · Private Library';
    } else if (state.view === 'auth') {
      document.title = lang === 'zh'
        ? (state.authMode === 'setup' ? '初始化 · 墨阅' : '登录 · 墨阅')
        : (state.authMode === 'setup' ? 'Setup · Moyue' : 'Sign in · Moyue');
    }
    // 页码、朗读状态等是运行时拼出来的，切语言后重新生成一次。
    if (state.view === 'reader' && state.current) {
      syncSettingsUI();
      updateChrome();
      if (state.tts.active) renderTTSBar();
      if (isAudio(state.current)) {
        state.audioReady = false;
        state.audioBook = '';
        loadChapter(state.chapter, state.fraction);
      } else if (state.current.type === 'pdf') {
        state.pdf.ready = false;
        state.pdf.loading = false;
        loadChapter(state.chapter, state.fraction);
      }
    } else if (state.view === 'library') {
      renderLibrarySwitch();
      renderStats();
      renderContinue();
      renderShelf();
    } else if (state.view === 'admin') {
      renderAdminLibraries();
      renderAdminUsers();
    }
    if (!el.uploadScrim.hidden) renderUploadList();
  }

  function syncLangUI() {
    const lang = normalizeLang(prefs.lang);
    if (el.langSeg) {
      el.langSeg.querySelectorAll('[data-lang]').forEach((b) =>
        b.classList.toggle('is-active', b.dataset.lang === lang));
    }
    if (el.authLang) el.authLang.value = lang;
    if (el.userLang) el.userLang.value = lang;
  }

  function applyAppTheme() {
    document.documentElement.dataset.theme = prefs.theme;
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute('content', THEMES[prefs.theme].ui);
  }

  function applyFrameStyle() {
    if (state.current && state.current.type === 'pdf') {
      postPDFTheme();
      return;
    }
    if (state.current && isAudio(state.current)) {
      postAudioTheme();
      return;
    }
    if (state.current && isFixed(state.current)) return;
    const doc = el.frame.contentDocument;
    if (!doc || !doc.body) return;
    const theme = THEMES[prefs.theme] || THEMES.paper;
    const compact = el.frame.clientWidth < 640;
    const padding = compact ? '20px 20px 96px' : '36px clamp(26px, 5vw, 56px) 110px';
    const filter = prefs.theme === 'night' ? 'filter:brightness(.82) contrast(1.02);' : '';

    let style = doc.getElementById('moyue-prefs');
    if (!style) {
      style = doc.createElement('style');
      style.id = 'moyue-prefs';
      doc.head.appendChild(style);
    }
    const hasOwnCSS = !!doc.querySelector('link[rel="stylesheet"], style:not(#moyue-prefs):not(#moyue-base)');
    style.textContent = `
      html { background:${theme.pageBg} !important; }
      body {
        background:${theme.pageBg} !important;
        color:${theme.ink} !important;
        font-family:${FONTS[prefs.font]} !important;
        font-size:${prefs.size}px !important;
        line-height:${prefs.line} !important;
        max-width:${prefs.measure}px !important;
        margin:0 auto !important;
        padding:${padding} !important;
        box-sizing:border-box;
        min-height:100%;
      }
      body * { max-width:100%; }
      a { color:${theme.link} !important; }
      img, svg, video, canvas { ${filter} }
      h1, h2, h3, h4 { color:${theme.ink} !important; }
      ::selection { background:${theme.sel}; }
    `;
    doc.documentElement.style.backgroundColor = theme.pageBg;

    style.textContent += `
      mark.moyue-hl { color: inherit; border-radius: 2px; padding: 0 1px; cursor: pointer;
        background: rgba(242, 214, 117, .55); box-shadow: 0 0 0 1px rgba(242, 214, 117, .25); }
      mark.moyue-hl[data-color="green"] { background: rgba(169, 214, 162, .55); box-shadow: 0 0 0 1px rgba(169, 214, 162, .25); }
      mark.moyue-hl[data-color="blue"] { background: rgba(156, 196, 232, .55); box-shadow: 0 0 0 1px rgba(156, 196, 232, .25); }
      mark.moyue-hl[data-color="pink"] { background: rgba(239, 179, 196, .58); box-shadow: 0 0 0 1px rgba(239, 179, 196, .25); }
      mark.moyue-hl.is-focus { outline: 2px solid currentColor; outline-offset: 2px; }
      mark.moyue-tts { color: inherit; border-radius: 3px; padding: 0 1px;
        background: color-mix(in srgb, ${theme.link} 26%, transparent);
        box-shadow: 0 0 0 1px ${theme.link}55; }
      .kindle-break { border: 0; height: 1px; background: currentColor; opacity: .12; margin: 2em auto; }
      .fb2-poem { margin: 1.2em 0; } .fb2-stanza { margin: .8em 0; }
      .fb2-v { text-indent: 0; margin: .3em 0; } .fb2-author { text-align: right; font-style: italic; }
    `;
    if (compact) {
      style.textContent += '\nbody { max-width:100% !important; }';
    }
    if (!hasOwnCSS) {
      style.textContent += `
        body > p { text-indent:2em; margin:0 0 1em; }
        body > h1, body > h2 { margin:1.4em 0 .8em; }
        body > blockquote { margin:1.4em 0; padding-left:1em; border-left:3px solid ${theme.link}33; }
      `;
    }
  }

  function syncSettingsUI() {
    if (el.userTheme) el.userTheme.value = prefs.theme;
    $$('.theme-chip', el.themeGrid).forEach((c) => c.classList.toggle('is-active', c.dataset.theme === prefs.theme));
    $$('button', el.fontSeg).forEach((b) => b.classList.toggle('is-active', b.dataset.font === prefs.font));
    $$('button', el.modeSeg).forEach((b) => b.classList.toggle('is-active', b.dataset.mode === prefs.mode));
    const hint = el.modeSeg.parentElement?.querySelector('.setting-hint');
    if (hint) {
      hint.textContent = tr(prefs.mode === 'spread' && !isSpreadWide()
        ? '当前窗口较窄，双页会先以单页显示；放大窗口后自动恢复双页。'
        : '双页仅在大屏生效；选择上下滚动时不会分栏。');
    }

    el.sizeRange.value = String(prefs.size);
    el.sizeVal.textContent = `${prefs.size} px`;
    el.sizeDown.disabled = prefs.size <= 14;
    el.sizeUp.disabled = prefs.size >= 30;
    setFill(el.sizeRange, (prefs.size - 14) / (30 - 14) * 100);

    syncLangUI();

    el.lineRange.value = String(Math.round(prefs.line * 10));
    el.lineVal.textContent = prefs.line.toFixed(1);
    setFill(el.lineRange, (prefs.line - 1.5) / (2.3 - 1.5) * 100);

    el.measureRange.value = String(prefs.measure);
    el.measureVal.textContent = `${prefs.measure} px`;
    setFill(el.measureRange, (prefs.measure - 560) / (1000 - 560) * 100);
  }

  function setFill(input, percent) {
    input.style.setProperty('--fill', `${clamp(percent, 0, 100)}%`);
  }

  function updateChrome() {
    const book = state.current;
    if (!book) return;
    const total = book.chapters.length;
    const chapter = book.chapters[state.chapter];
    const audio = isAudio(book);
    const fixed = isFixed(book);
    const paged = isPaginated() && state.page.active;
    el.ttsBtn.hidden = !isTextBook(book);
    if (audio) {
      el.rChapter.textContent = tr(`有声书 · ${chapter.title}`);
    } else if (fixed) {
      el.rChapter.textContent = tr(pageLabel(book));
    } else if (paged) {
      el.rChapter.textContent = tr(`第 ${state.chapter + 1} / ${total} 节 · 第 ${state.page.page + 1} / ${state.page.pages} 页 · ${chapter.title}`);
    } else {
      el.rChapter.textContent = tr(`第 ${state.chapter + 1} / ${total} 节 · ${chapter.title}`);
    }
    const pageMode = paged || fixed;
    const prevLabel = tr(audio ? '上一段' : pageMode ? '上一页' : '上一章');
    const nextLabel = tr(audio ? '下一段' : pageMode ? '下一页' : '下一章');
    el.prevBtn.querySelector('.btn-label').textContent = prevLabel;
    el.nextBtn.querySelector('.btn-label').textContent = nextLabel;
    el.prevBtn.title = prevLabel;
    el.nextBtn.title = nextLabel;
    const step = chapterStep();
    const finished = !!(book.progress && book.progress.finished);
    const atEnd = paged
      ? state.chapter >= total - 1 && state.page.page >= state.page.pages - 1
      : state.chapter + step > total - 1;
    const canFinish = atEnd && (paged || fixed || state.fraction >= 0.985);
    el.prevBtn.disabled = paged
      ? state.chapter <= 0 && state.page.page === 0
      : state.chapter <= 0;
    el.nextBtn.disabled = atEnd && (!canFinish || finished);
    updateTOCActive();
  }

  function overallPercent() {
    const total = state.current?.chapters.length || 1;
    return clamp((state.chapter + state.fraction) / total * 100, 0, 100);
  }

  function updateProgressUI() {
    const percent = overallPercent();
    state.percent = percent;
    el.range.value = String(Math.round(percent * 10));
    setFill(el.range, percent);
    el.progressLabel.textContent = percent < 0.05
      ? '0%'
      : `${percent >= 10 ? Math.round(percent) : percent.toFixed(1)}%`;
    const total = state.current?.chapters.length || 1;
    el.nextHint.classList.toggle('is-visible', !isPaginated() && !isFixed(state.current) && !isAudio(state.current)
      && state.fraction > 0.965 && state.chapter < total - 1);
    updateBookmarkState();
  }

  function fadeFrameOut() {
    el.frame.classList.add('is-swapping');
    clearTimeout(state.frameFadeTimer);
    // 加载失败或文档为空时也要恢复，避免一直看不见。
    state.frameFadeTimer = setTimeout(fadeFrameIn, 2500);
  }

  function fadeFrameIn() {
    clearTimeout(state.frameFadeTimer);
    el.frame.classList.remove('is-swapping');
  }

  async function loadChapter(index, fraction = 0) {
    const book = state.current;
    if (!book) return;
    hideAnnoBar();
    const total = book.chapters.length;
    index = clamp(index, 0, total - 1);

    state.chapter = index;
    state.fraction = clamp(fraction, 0, 1);
    state.pendingFraction = state.fraction;
    updateChrome();
    updateProgressUI();
    renderTOC();

    if (isAudio(book)) {
      if (state.audioReady && state.audioBook === book.id) {
        postAudioSeek(state.fraction);
      } else {
        state.audioReady = false;
        state.audioBook = book.id;
        const q = new URLSearchParams({
          title: book.title || '',
          author: book.creator || '',
          start: String(state.fraction),
          lang: prefs.lang || 'zh',
        });
        fadeFrameOut();
        el.frame.src = `/api/books/${book.id}/audioview?${q.toString()}`;
      }
      scheduleSave();
      return;
    }

    if (book.type === 'pdf') {
      const wantSpread = prefs.mode === 'spread' && isSpreadWide();
      if (state.pdf.spread !== wantSpread) {
        state.pdf.ready = false;
        state.pdf.loading = false;
        state.pdf.spread = wantSpread;
      }
      if (state.pdf.ready && el.frame.contentWindow) {
        el.frame.contentWindow.postMessage({ type: 'moyue-pdf-go', page: index + 1 }, '*');
      } else if (!state.pdf.loading) {
        state.pdf.loading = true;
        fadeFrameOut();
        el.frame.src = `/api/books/${book.id}/pdfview?page=${index + 1}${wantSpread ? '&spread=1' : ''}&lang=${encodeURIComponent(prefs.lang || 'zh')}`;
      }
      scheduleSave();
      return;
    }

    if (isFixed(book)) {
      state.comicSpread = prefs.mode === 'spread' && isSpreadWide();
      fadeFrameOut();
      el.frame.src = `/api/books/${book.id}/chapter/${index}${state.comicSpread ? '?spread=1' : ''}`;
      scheduleSave();
      return;
    }

    fadeFrameOut();
    el.frame.src = `/api/books/${book.id}/chapter/${index}`;
    scheduleSave();
  }

  function postPDFTheme() {
    if (!state.current || state.current.type !== 'pdf') return;
    const win = el.frame.contentWindow;
    if (!win) return;
    const theme = THEMES[prefs.theme] || THEMES.paper;
    win.postMessage({
      type: 'moyue-pdf-theme',
      theme: { bg: theme.pageBg, ink: theme.ink, muted: '#8a8175' },
    }, '*');
  }

  function postAudioTheme() {
    if (!isAudio(state.current)) return;
    const win = el.frame.contentWindow;
    if (!win) return;
    const theme = THEMES[prefs.theme] || THEMES.paper;
    win.postMessage({
      type: 'moyue-audio-theme',
      theme: {
        bg: theme.pageBg,
        card: theme.ui,
        ink: theme.ink,
        muted: prefs.theme === 'night' ? '#948c7e' : '#7d7468',
        line: prefs.theme === 'night' ? '#2b2f36' : '#e2dbcd',
        accent: theme.link,
        accentInk: prefs.theme === 'night' ? '#10201b' : '#f6f3ea',
      },
    }, '*');
  }

  function postAudioSeek(fraction) {
    if (!isAudio(state.current)) return;
    const win = el.frame.contentWindow;
    if (win) win.postMessage({ type: 'moyue-audio-seek', fraction }, '*');
  }

  function bindComicFrame(doc) {
    const win = el.frame.contentWindow;
    doc.addEventListener('click', (event) => {
      if (event.target.closest && event.target.closest('a')) return;
      const width = win.innerWidth || 1;
      if (event.clientX > width * 0.65) nextChapter();
      else if (event.clientX < width * 0.35) prevChapter();
    });
    win.addEventListener('scroll', () => handleFrameScroll(), { passive: true });
    doc.addEventListener('scroll', () => handleFrameScroll(), { passive: true });
    doc.addEventListener('keydown', onFrameKey);
  }

  window.addEventListener('message', (event) => {
    const data = event.data;
    if (!data || typeof data.type !== 'string' ||
      (!data.type.startsWith('moyue-pdf-') && !data.type.startsWith('moyue-audio-'))) return;
    if (data.type === 'moyue-audio-ready') {
      state.audioReady = true;
      postAudioTheme();
      return;
    }
    if (data.type === 'moyue-audio-time') {
      if (!isAudio(state.current)) return;
      state.fraction = clamp(Number(data.fraction) || 0, 0, 1);
      updateProgressUI();
      scheduleSave();
      return;
    }
    if (data.type === 'moyue-audio-error') {
      toast(tr(data.message || '音频播放失败'), { type: 'error' });
      return;
    }
    if (data.type === 'moyue-pdf-ready') {
      state.pdf.ready = data.pages > 0;
      state.pdf.loading = false;
      state.pdf.spread = !!data.spread;
      if (data.pages > 0) {
        state.pdf.pages = data.pages;
      } else {
        toast(tr('PDF 加载失败') + (data.error ? '：' + data.error : ''), { type: 'error' });
        return;
      }
      postPDFTheme();
      if (state.current && state.current.type === 'pdf' && el.frame.contentWindow) {
        el.frame.contentWindow.postMessage({ type: 'moyue-pdf-go', page: state.chapter + 1 }, '*');
      }
    } else if (data.type === 'moyue-pdf-page') {
      if (!state.current || state.current.type !== 'pdf') return;
      const index = clamp((Number(data.page) || 1) - 1, 0, state.current.chapters.length - 1);
      if (typeof data.spread === 'boolean' && data.spread !== state.pdf.spread) {
        state.pdf.spread = data.spread;
        updateChrome();
      }
      if (index !== state.chapter) {
        state.chapter = index;
        updateChrome();
        updateProgressUI();
        renderTOC();
      }
      scheduleSave();
    }
  });

  function bindFrame() {
    let doc;
    try {
      doc = el.frame.contentDocument;
    } catch {
      return;
    }
    if (!doc || !doc.body) return;
    const win = el.frame.contentWindow;

    win.addEventListener('scroll', () => handleFrameScroll(), { passive: true });
    doc.addEventListener('scroll', () => handleFrameScroll(), { passive: true });
    doc.addEventListener('click', onFrameClick);
    doc.addEventListener('keydown', onFrameKey);
    doc.addEventListener('mouseup', handleSelection);
    doc.addEventListener('touchend', handleSelection, { passive: true });
    doc.addEventListener('scroll', hideAnnoBar, { passive: true });
    doc.addEventListener('wheel', onFrameWheel, { passive: false });
    doc.addEventListener('touchstart', onFrameTouchStart, { passive: true });
    doc.addEventListener('touchend', onFrameTouchEnd, { passive: true });
  }

  let wheelLock = 0;

  function onFrameWheel(event) {
    if (!isPaginated() || !state.page.active) return;
    if (Math.abs(event.deltaY) < 6 && Math.abs(event.deltaX) < 6) return;
    event.preventDefault();
    if (Date.now() - wheelLock < 360) return;
    wheelLock = Date.now();
    advance((event.deltaY || event.deltaX) > 0 ? 1 : -1);
  }

  let frameTouchX = 0;
  let frameTouchY = 0;

  function onFrameTouchStart(event) {
    const t = event.touches[0];
    if (!t) return;
    frameTouchX = t.clientX;
    frameTouchY = t.clientY;
  }

  function onFrameTouchEnd(event) {
    if (!isPaginated() || !state.page.active) return;
    const t = event.changedTouches[0];
    if (!t) return;
    const dx = t.clientX - frameTouchX;
    const dy = t.clientY - frameTouchY;
    if (Math.abs(dx) > 52 && Math.abs(dx) > Math.abs(dy) * 1.15) {
      advance(dx < 0 ? 1 : -1);
    }
  }

  function onFrameKey(event) {
    if (handleKey(event)) event.preventDefault();
  }

  function onFrameClick(event) {
    const mark = event.target.closest && event.target.closest('mark.moyue-hl');
    if (mark) {
      const a = (state.current?.annotations || []).find((x) => x.id === mark.dataset.id);
      if (a) {
        hideAnnoBar();
        openNoteDialog(a);
      }
      return;
    }
    const anchor = event.target.closest && event.target.closest('a');
    if (!anchor) {
      if (isPaginated() && state.page.active) {
        const win = el.frame.contentWindow;
        const selection = win?.getSelection?.();
        if (selection && !selection.isCollapsed && selection.toString().trim()) return;
        const width = win?.innerWidth || 1;
        if (event.clientX > width * 0.72) advance(1);
        else if (event.clientX < width * 0.28) advance(-1);
      }
      return;
    }
    const href = anchor.getAttribute('href') || '';
    if (href.startsWith('#moyue-chapter-')) {
      event.preventDefault();
      const index = parseInt(href.replace('#moyue-chapter-', ''), 10);
      if (!Number.isNaN(index)) {
        closeDrawers();
        loadChapter(index, 0);
      }
    } else if (/^https?:\/\//i.test(href)) {
      anchor.setAttribute('target', '_blank');
      anchor.setAttribute('rel', 'noopener noreferrer');
    }
  }

  function handleFrameScroll() {
    if (isAudio(state.current)) return;
    if (state.page.active && isPaginated()) return;
    if (state.scrollRaf) return;
    state.scrollRaf = requestAnimationFrame(() => {
      state.scrollRaf = 0;
      const doc = el.frame.contentDocument;
      const win = el.frame.contentWindow;
      if (!doc || !win) return;
      const scroller = doc.scrollingElement || doc.documentElement;
      const max = Math.max(0, scroller.scrollHeight - win.innerHeight);
      state.fraction = max > 0 ? clamp(win.scrollY / max, 0, 1) : 0;
      updateProgressUI();
      scheduleSave();
    });
  }

  function restoreScroll(fraction) {
    const doc = el.frame.contentDocument;
    const win = el.frame.contentWindow;
    if (!doc || !win) return;
    const scroller = doc.scrollingElement || doc.documentElement;
    const max = Math.max(0, scroller.scrollHeight - win.innerHeight);
    win.scrollTo(0, Math.round(max * clamp(fraction, 0, 1)));
  }

  /* ---------------------------- 分页排版 ---------------------------- */

  function clearPagination() {
    const doc = el.frame.contentDocument;
    if (doc) {
      doc.getElementById('moyue-paged')?.remove();
      if (doc.body) {
        doc.body.style.transform = '';
        doc.body.style.transition = '';
      }
    }
    el.stage.classList.remove('is-paged');
    el.frame.style.width = '';
    el.frame.style.margin = '';
    state.page.active = false;
    state.page.page = 0;
    state.page.pages = 1;
    state.page.vw = 0;
  }

  // paginateText 把正文排成横向分栏：单页一栏，宽屏双页两栏。
  function paginateText(fraction = state.fraction) {
    const book = state.current;
    if (!isTextBook(book) || prefs.mode === 'scroll') return;
    const doc = el.frame.contentDocument;
    const win = el.frame.contentWindow;
    if (!doc || !doc.body) return;

    const stageW = el.stage.clientWidth || win.innerWidth || 800;
    const stageH = el.stage.clientHeight || win.innerHeight || 600;
    const spread = prefs.mode === 'spread' && stageW >= SPREAD_MIN;
    const margin = stageW < 640 ? 14 : 34;
    const gutter = spread
      ? Math.round(clamp(stageW * 0.055, 44, 90))
      : Math.round(clamp(stageW * 0.045, 28, 64));
    const measure = clamp(Number(prefs.measure) || 760, 480, 1000);
    const maxW = spread ? measure * 2 + gutter : measure;
    const contentW = Math.max(260, Math.round(Math.min(stageW - margin * 2, maxW)));
    const sidePad = Math.round(gutter / 2);
    // 栏宽按去掉左右页边距后的可用宽度计算，
    // 两栏与栏间距之和不超过可用宽度，保证每屏恰好两栏。
    const available = contentW - sidePad * 2;
    const colW = spread
      ? Math.max(160, Math.floor((available - gutter) / 2))
      : available;
    const padTop = stageW < 640 ? 16 : 32;
    const padBottom = stageW < 640 ? 26 : 38;
    const theme = THEMES[prefs.theme] || THEMES.paper;

    el.stage.classList.add('is-paged');
    el.frame.style.width = `${contentW}px`;
    el.frame.style.margin = '0 auto';

    let style = doc.getElementById('moyue-paged');
    if (!style) {
      style = doc.createElement('style');
      style.id = 'moyue-paged';
      doc.head.appendChild(style);
    }
    style.textContent = `
      html { height:100% !important; overflow:hidden !important; background:${theme.pageBg} !important; }
      body {
        height:100% !important;
        width:auto !important;
        max-width:none !important;
        min-height:0 !important;
        margin:0 !important;
        padding:${padTop}px ${sidePad}px ${padBottom}px !important;
        column-width:${colW}px !important;
        column-gap:${gutter}px !important;
        column-fill:auto !important;
        overflow:visible !important;
        background:transparent !important;
        box-sizing:border-box !important;
      }
      body * { max-width:100% !important; }
      img, svg, video, pre, table, blockquote, figure { break-inside:avoid; }
      h1, h2, h3, h4 { break-after:avoid; }
    `;

    const body = doc.body;
    body.style.transition = 'none';
    body.style.transform = 'translate3d(0, 0, 0)';

    // 翻页步长取实际栏距，与排版时使用的宽度保持一致。
    const step = available + gutter;
    const scrollWidth = Math.max(body.scrollWidth, contentW);
    const pages = Math.max(1, Math.ceil((scrollWidth - 1) / step));

    state.page.active = true;
    state.page.pages = pages;
    state.page.vw = step;
    state.page.page = 0;
    const target = Math.round(clamp(fraction, 0, 1) * (pages - 1));
    // 单页章节没有“页内位置”可表达；保留已读完状态，避免把 fraction=1 写成 0。
    setPage(target, { save: false, onePageFraction: clamp(fraction, 0, 1) });

    requestAnimationFrame(() => {
      if (state.page.active && doc.body) doc.body.style.transition = 'transform .22s ease';
    });
  }

  function setPage(index, { save = true, onePageFraction = 0 } = {}) {
    if (!state.page.active) return;
    const total = Math.max(1, state.page.pages);
    const page = clamp(index, 0, total - 1);
    state.page.page = page;
    state.fraction = total > 1 ? page / (total - 1) : clamp(onePageFraction, 0, 1);
    const doc = el.frame.contentDocument;
    if (doc && doc.body) {
      const offset = Math.round(page * state.page.vw);
      doc.body.style.transform = `translate3d(${-offset}px, 0, 0)`;
    }
    updateChrome();
    updateProgressUI();
    if (save) scheduleSave();
  }

  // advance 在当前章节内翻页；到达本章首尾时切换到相邻章节。
  function advance(delta) {
    if (!state.current) return;
    if (isPaginated() && state.page.active) {
      const target = state.page.page + delta;
      if (target >= 0 && target < state.page.pages) {
        setPage(target);
        return;
      }
    }
    if (delta > 0) nextChapter();
    else prevChapter();
  }

  function chapterStep() {
    const book = state.current;
    if (!book) return 1;
    if (book.type === 'pdf') return state.pdf.spread ? 2 : 1;
    if (isFixed(book)) return state.comicSpread ? 2 : 1;
    return 1;
  }

  function pageLabel(book) {
    if (book.type === 'pdf') {
      const total = book.chapters.length;
      if (state.pdf.spread) {
        const last = Math.min(state.chapter + 2, total);
        return `第 ${state.chapter + 1}–${last} / ${total} 页`;
      }
      return `第 ${state.chapter + 1} / ${total} 页`;
    }
    const total = book.chapters.length;
    if (state.comicSpread) {
      const last = Math.min(state.chapter + 2, total);
      return `第 ${state.chapter + 1}–${last} / ${total} 页`;
    }
    return `第 ${state.chapter + 1} / ${total} 页`;
  }

  function onFrameLoad() {
    if (state.view !== 'reader' || !state.current) return;
    const book = state.current;
    if (isAudio(book)) {
      postAudioTheme();
      updateChrome();
      updateProgressUI();
      fadeFrameIn();
      return;
    }
    if (book.type === 'pdf') {
      postPDFTheme();
      updateChrome();
      updateProgressUI();
      fadeFrameIn();
      return;
    }

    const doc = el.frame.contentDocument;
    if (!doc || !doc.body || !doc.body.innerHTML.trim()) {
      fadeFrameIn();
      return;
    }

    if (isFixed(book)) {
      bindComicFrame(doc);
      updateChrome();
      updateProgressUI();
      fadeFrameIn();
      return;
    }

    applyFrameStyle();
    bindFrame();
    applyAnnotationsForChapter();

    const fraction = state.pendingFraction;
    if (prefs.mode !== 'scroll') {
      clearPagination();
      paginateText(fraction);
    } else {
      clearPagination();
      restoreScroll(fraction);
      requestAnimationFrame(() => restoreScroll(fraction));
      setTimeout(() => restoreScroll(fraction), 180);
      setTimeout(() => restoreScroll(fraction), 500);
    }
    fadeFrameIn();
    updateChrome();
    updateProgressUI();

    if (state.tts.active) {
      refreshTTSChapter(true);
    }

    if (state.pendingAnnotation) {
      const id = state.pendingAnnotation;
      state.pendingAnnotation = '';
      setTimeout(() => focusAnnotation(id, null), 550);
    }
    if (state.pendingSearch && state.pendingSearch.chapter === state.chapter) {
      const { q, percent } = state.pendingSearch;
      state.pendingSearch = null;
      setTimeout(() => scrollToQuery(q, percent), 650);
    }
  }

  function scheduleSave() {
    clearTimeout(state.saveTimer);
    state.saveTimer = setTimeout(() => saveProgress(), 900);
  }

  function syncAudioFraction() {
    if (!isAudio(state.current)) return;
    try {
      const a = el.frame.contentDocument?.querySelector('audio');
      if (a && Number.isFinite(a.duration) && a.duration > 0) {
        state.fraction = clamp(a.currentTime / a.duration, 0, 1);
      }
    } catch { /* 忽略 */ }
  }

  async function saveProgress() {
    const book = state.current;
    if (!book) return;
    syncAudioFraction();
    const key = `${state.chapter}:${state.fraction.toFixed(4)}`;
    if (key === state.lastSaved) return;
    state.lastSaved = key;

    const payload = { chapter: state.chapter, chapterPercent: state.fraction };
    const cached = state.books.find((b) => b.id === book.id);
    if (cached) {
      cached.progress = {
        ...(cached.progress || {}),
        chapter: state.chapter,
        chapterPercent: state.fraction,
        percent: overallPercent(),
        updatedAt: new Date().toISOString(),
      };
    }
    try {
      const progress = await api.progress(book.id, payload);
      book.progress = progress;
      if (cached) cached.progress = { ...cached.progress, ...progress };
    } catch { /* 离线或会话过期时静默 */ }
  }

  function flushProgress() {
    clearTimeout(state.saveTimer);
    const book = state.current;
    if (!book) return;
    syncAudioFraction();
    const body = JSON.stringify({ chapter: state.chapter, chapterPercent: state.fraction });
    try {
      fetch(`/api/books/${book.id}/progress`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body,
        keepalive: true,
      });
    } catch { /* 忽略 */ }
  }

  function accumulateTime() {
    const now = Date.now();
    const delta = (now - state.lastTick) / 1000;
    state.lastTick = now;
    if (delta > 0 && delta < 30) state.activeSeconds += delta;
  }

  function startHeartbeat() {
    stopHeartbeat();
    state.activeSeconds = 0;
    state.lastTick = Date.now();
    state.heartbeat = setInterval(() => {
      if (state.view !== 'reader' || !state.current || document.visibilityState !== 'visible') {
        state.lastTick = Date.now();
        return;
      }
      accumulateTime();
      if (state.activeSeconds >= 30) {
        const seconds = Math.round(state.activeSeconds);
        state.activeSeconds = 0;
        api.read(state.current.id, seconds).catch(() => {});
      }
    }, 10000);
  }

  function stopHeartbeat() {
    clearInterval(state.heartbeat);
    state.heartbeat = 0;
  }

  function flushReadingTime() {
    const book = state.current;
    if (!book) return;
    accumulateTime();
    const seconds = Math.round(state.activeSeconds);
    if (seconds <= 0) return;
    state.activeSeconds = 0;
    try {
      fetch(`/api/books/${book.id}/read`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ seconds }),
        keepalive: true,
      });
    } catch { /* 忽略 */ }
  }

  async function openBook(id, options = {}) {
    if (state.loading) return;
    if (state.view === 'reader' && state.current && state.current.id !== id) {
      stopTTS();
      saveProgress();
      flushProgress();
      flushReadingTime();
      stopHeartbeat();
      closeDrawers();
    }
    state.loading = true;
    closeMenus();
    closeDrawers();
    el.dropOverlay.hidden = true;
    try {
      const detail = await api.detail(id);
      if (!detail.chapters || detail.chapters.length === 0) throw new Error('这本书没有可阅读的章节');

      state.current = detail;
      state.searchSeq++;
      const total = detail.chapters.length;
      state.chapter = options.chapter ?? clamp(detail.progress?.chapter || 0, 0, total - 1);
      state.fraction = options.fraction ?? clamp(detail.progress?.chapterPercent || 0, 0, 1);
      state.pendingFraction = state.fraction;
      state.percent = detail.progress?.percent || 0;
      state.lastSaved = `${state.chapter}:${state.fraction.toFixed(4)}`;
      state.pdf = {
        ready: false,
        loading: false,
        pages: detail.type === 'pdf' ? total : 0,
        bookId: detail.type === 'pdf' ? detail.id : '',
        spread: false,
      };
      state.page = { active: false, page: 0, pages: 1, vw: 0 };
      state.comicSpread = false;
      state.audioReady = false;
      state.audioBook = '';
      state.view = 'reader';

      document.body.classList.add('reading');
      el.library.hidden = true;
      el.admin.hidden = true;
      el.authView.hidden = true;
      el.reader.hidden = false;
      el.rTitle.textContent = detail.title;
      document.title = `${detail.title} · ${brandName()}`;
      renderTOC();
      renderBookmarks();
      renderNotes();
      syncSettingsUI();
      state.selection = null;
      state.pendingAnnotation = '';
      state.pendingSearch = null;
      state.searchDone = false;
      hideAnnoBar();
      el.searchInputInBook.value = '';
      el.searchMeta.textContent = '';
      el.searchResults.innerHTML = '';
      updateChrome();
      updateProgressUI();

      if (location.hash !== `#/read/${id}`) {
        history.pushState({ book: id }, '', `#/read/${id}`);
      }

      loadChapter(state.chapter, state.fraction);
      startHeartbeat();
      loadTTSStatus();
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    } finally {
      state.loading = false;
    }
  }

  function exitReader({ updateHistory = true } = {}) {
    if (state.view !== 'reader') return;
    fadeFrameIn();
    stopTTS();
    saveProgress();
    flushProgress();
    flushReadingTime();
    stopHeartbeat();
    closeDrawers();
    hideAnnoBar();
    closeNoteDialog();
    if (updateHistory && location.hash) {
      history.pushState(null, '', location.pathname);
    }
    enterLibrary({ updateHistory: false });
  }

  async function markFinished() {
    const book = state.current;
    if (!book || !book.chapters.length) return;
    const last = book.chapters.length - 1;
    state.chapter = last;
    state.fraction = 1;

    const cached = state.books.find((b) => b.id === book.id);
    const optimistic = {
      ...(book.progress || {}),
      chapter: last,
      chapterPercent: 1,
      percent: 100,
      finished: true,
      updatedAt: new Date().toISOString(),
    };
    book.progress = optimistic;
    if (cached) cached.progress = { ...(cached.progress || {}), ...optimistic };
    updateChrome();
    updateProgressUI();

    try {
      const progress = await api.progress(book.id, { chapter: last, chapterPercent: 1 });
      book.progress = progress;
      if (cached) cached.progress = progress;
      state.lastSaved = `${last}:1.0000`;
    } catch {
      state.lastSaved = '';
    }
    toast(tr('已读完'));
  }

  function nextChapter() {
    const book = state.current;
    if (!book) return;
    const step = chapterStep();
    if (state.chapter + step > book.chapters.length - 1) {
      if (book.progress?.finished && state.fraction >= 1) {
        toast(tr('已读完'));
        return;
      }
      const atContentEnd = isPaginated() || isFixed(book) || state.fraction >= 0.985;
      if (!atContentEnd) {
        toast(isFixed(book) ? '已经是最后一页了' : '已经是最后一节了');
        return;
      }
      markFinished();
      return;
    }
    loadChapter(state.chapter + step, 0);
  }

  function prevChapter() {
    const book = state.current;
    if (!book) return;
    if (state.chapter <= 0) {
      toast(isFixed(book) ? '已经是第一页了' : '已经是第一节了');
      return;
    }
    const step = chapterStep();
    // 从下一章倒回到上一章末尾时，分页模式落在最后一页。
    loadChapter(Math.max(0, state.chapter - step), isPaginated() ? 1 : 0);
  }

  function bookmarkAtCurrent() {
    const list = state.current?.bookmarks || [];
    if (!list.length) return null;
    const tolerance = isPaginated() && state.page.active
      ? Math.max(0.01, 0.5 / Math.max(1, state.page.pages - 1))
      : 0.012;
    return list.find((m) => m.chapter === state.chapter
      && Math.abs((m.percent || 0) - state.fraction) <= tolerance) || null;
  }

  function updateBookmarkState() {
    if (!el.markBtn) return;
    const marked = !!bookmarkAtCurrent();
    el.markBtn.classList.toggle('is-on', marked);
    const label = marked ? '取消书签 (B)' : '书签 (B)';
    el.markBtn.title = label;
    el.markBtn.setAttribute('aria-label', marked ? '取消书签' : '书签');
  }

  async function toggleBookmark() {
    const book = state.current;
    if (!book || state.bookmarkBusy) return;
    const existing = bookmarkAtCurrent();
    state.bookmarkBusy = true;
    try {
      if (existing) {
        await api.removeBookmark(book.id, existing.id);
        book.bookmarks = (book.bookmarks || []).filter((m) => m.id !== existing.id);
        renderBookmarks();
        updateBookmarkState();
        toast('已取消书签');
        return;
      }
      const chapter = book.chapters[state.chapter];
      const mark = await api.addBookmark(book.id, {
        chapter: state.chapter,
        chapterTitle: chapter.title,
        percent: state.fraction,
        snippet: extractSnippet(),
      });
      book.bookmarks = [mark, ...(book.bookmarks || [])];
      renderBookmarks();
      updateBookmarkState();
      toast('已添加书签');
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    } finally {
      state.bookmarkBusy = false;
    }
  }

  function extractSnippet() {
    if (isAudio(state.current)) {
      return `音频 · ${Math.round(state.fraction * 100)}%`;
    }
    const doc = el.frame.contentDocument;
    const win = el.frame.contentWindow;
    if (!doc || !win) return '';
    let node = null;
    try {
      node = doc.elementFromPoint(Math.min(win.innerWidth / 2, win.innerWidth - 8), win.innerHeight * 0.32);
    } catch { /* 忽略 */ }
    while (node && !/^(P|H1|H2|H3|H4|H5|LI|BLOCKQUOTE|TD|DD|DIV)$/.test(node.tagName)) {
      node = node.parentElement;
    }
    let text = node ? node.textContent : doc.body.textContent;
    text = String(text || '').replace(/\s+/g, ' ').trim();
    if (text.length > 64) text = text.slice(0, 64) + '…';
    return text || `第 ${state.chapter + 1} 节 · ${Math.round(state.fraction * 100)}%`;
  }

  function jumpTo(value) {
    const book = state.current;
    if (!book) return;
    if (isAudio(book)) {
      state.fraction = clamp(value / 1000, 0, 1);
      state.audioReady = true;
      postAudioSeek(state.fraction);
      updateProgressUI();
      scheduleSave();
      return;
    }
    const total = book.chapters.length;
    let target = clamp(value / 1000, 0, 0.999999) * total;
    let chapter = Math.floor(target);
    let fraction = target - chapter;
    if (chapter >= total) {
      chapter = total - 1;
      fraction = 1;
    }
    loadChapter(chapter, fraction);
  }

  async function toggleFullscreen() {
    try {
      if (!document.fullscreenElement) {
        await el.reader.requestFullscreen?.();
      } else {
        await document.exitFullscreen?.();
      }
    } catch {
      toast('当前浏览器不支持全屏', { type: 'error' });
    }
  }

  document.addEventListener('fullscreenchange', () => {
    const on = !!document.fullscreenElement;
    el.fullBtn.querySelector('use').setAttribute('href', on ? '#i-shrink' : '#i-expand');
  });

  /* ============================ 语音朗读 ============================ */

  const MIMO_VOICES = [
    ['mimo_default', '默认'], ['冰糖', '冰糖'], ['茉莉', '茉莉'], ['苏打', '苏打'], ['白桦', '白桦'],
    ['Mia', 'Mia'], ['Chloe', 'Chloe'], ['Milo', 'Milo'], ['Dean', 'Dean'],
  ];
  const OPENAI_VOICES = [
    ['alloy', 'alloy'], ['echo', 'echo'], ['fable', 'fable'], ['onyx', 'onyx'],
    ['nova', 'nova'], ['shimmer', 'shimmer'],
  ];

  async function loadTTSStatus() {
    try {
      state.tts.status = await api.ttsStatus();
    } catch {
      state.tts.status = { enabled: false, voices: [] };
    }
    fillTTSVoices();
    updateTTSButton();
  }

  function ttsEnabled() {
    return !!state.tts.status?.enabled;
  }

  function updateTTSButton() {
    const ok = ttsEnabled();
    el.ttsBtn.classList.toggle('is-off', !ok);
    el.ttsBtn.title = ok ? '语音朗读 (R)' : '语音朗读（管理员尚未配置）';
    el.ttsBtn.setAttribute('aria-label', el.ttsBtn.title);
  }

  function fillTTSVoices() {
    const status = state.tts.status || {};
    const voices = Array.isArray(status.voices) && status.voices.length
      ? status.voices
      : MIMO_VOICES.map(([id, name]) => ({ id, name }));
    if (!voices.some((v) => v.id === prefs.ttsVoice)) {
      prefs.ttsVoice = status.defaultVoice || voices[0]?.id || 'mimo_default';
    }
    el.ttsVoice.innerHTML = voices.map((v) =>
      `<option value="${esc(v.id)}">${esc(v.name || v.id)}</option>`).join('');
    el.ttsVoice.value = prefs.ttsVoice;
    el.ttsRate.value = String(prefs.ttsRate || 1);
  }

  function splitSentences(text) {
    const out = [];
    const enders = '。！？!?…';
    const closers = '”’"\'）)》】」』';
    let start = -1;
    let lastSoft = -1;

    const push = (s, e) => {
      if (e <= s) return;
      const raw = text.slice(s, e);
      if (!raw.trim()) return;
      out.push({ start: s, end: e });
    };

    for (let i = 0; i < text.length; i++) {
      const ch = text[i];
      if (start < 0) {
        if (/\s/.test(ch)) continue;
        start = i;
        lastSoft = -1;
      }
      if (ch === '\n') {
        if (i - start >= 18) {
          push(start, i);
          start = -1;
        }
        continue;
      }
      const sentenceDot = ch === '.' && !/\d/.test(text[i - 1] || '')
        && (i + 1 >= text.length || /\s/.test(text[i + 1]));
      if (enders.includes(ch) || sentenceDot) {
        let end = i + 1;
        while (end < text.length && closers.includes(text[end])) end++;
        push(start, end);
        i = end - 1;
        start = -1;
        continue;
      }
      if ('，,；;：:、'.includes(ch)) lastSoft = i + 1;
      if (i - start >= 150) {
        const cut = lastSoft > start + 30 ? lastSoft : i + 1;
        push(start, cut);
        start = -1;
        i = cut - 1;
      }
    }
    if (start >= 0) push(start, text.length);

    // 过短的碎片（如页码、单字）并入下一句，避免听起来断续。
    const merged = [];
    for (const item of out) {
      const len = item.end - item.start;
      const prev = merged[merged.length - 1];
      if (prev && len < 5 && item.end - prev.start <= 160) {
        prev.end = item.end;
      } else {
        merged.push({ ...item });
      }
    }
    return merged;
  }

  // 把相邻句子合并成 300–400 字的朗读块，减少请求次数；
  // 块内仍保留每句的偏移，播放时按进度逐句高亮。
  function buildTTSChunks(sentences) {
    const chunks = [];
    let current = null;
    for (const sentence of sentences) {
      if (!current) {
        current = {
          text: sentence.text,
          start: sentence.start,
          end: sentence.end,
          parts: [{ ...sentence, charStart: 0, charEnd: sentence.text.length }],
        };
        continue;
      }
      const needsSpace = /[A-Za-z0-9.,!?;:'")\]]$/.test(current.text)
        && /^[A-Za-z0-9"'(\[]/.test(sentence.text);
      const gap = sentence.gap || '';
      const sep = gap.includes('\n') ? '\n' : (needsSpace ? ' ' : '');
      const nextLen = current.text.length + sep.length + sentence.text.length;
      if (current.text.length < 300 && nextLen <= 400) {
        const charStart = current.text.length + sep.length;
        current.text += sep + sentence.text;
        current.parts.push({ ...sentence, charStart, charEnd: current.text.length });
        current.end = sentence.end;
      } else {
        chunks.push(current);
        current = {
          text: sentence.text,
          start: sentence.start,
          end: sentence.end,
          parts: [{ ...sentence, charStart: 0, charEnd: sentence.text.length }],
        };
      }
    }
    if (current) chunks.push(current);
    return chunks;
  }

  function collectTTSSentences() {
    const book = state.current;
    if (!isTextBook(book)) return [];
    const doc = el.frame.contentDocument;
    if (!doc || !doc.body) return [];
    const nodes = textNodes(doc);
    const text = nodes.map((c) => c.node.nodeValue).join('');
    const sentences = splitSentences(text)
      .map((s) => ({ start: s.start, end: s.end, text: text.slice(s.start, s.end).trim() }))
      .filter((s) => s.text.length > 0);
    for (let i = 1; i < sentences.length; i++) {
      sentences[i].gap = text.slice(sentences[i - 1].end, sentences[i].start);
    }
    return buildTTSChunks(sentences);
  }

  function clearTTSHighlight() {
    const doc = el.frame.contentDocument;
    if (!doc) return;
    doc.querySelectorAll('mark.moyue-tts').forEach((mark) => {
      const parent = mark.parentNode;
      if (!parent) return;
      while (mark.firstChild) parent.insertBefore(mark.firstChild, mark);
      parent.removeChild(mark);
      parent.normalize();
    });
  }

  function highlightTTSSentence(sentence) {
    const doc = el.frame.contentDocument;
    if (!doc || !sentence) return;
    clearTTSHighlight();
    for (const tn of textNodes(doc)) {
      if (tn.end <= sentence.start || tn.start >= sentence.end) continue;
      const from = Math.max(0, sentence.start - tn.start);
      const to = Math.min(tn.node.nodeValue.length, sentence.end - tn.start);
      if (to <= from) continue;
      if (!tn.node.nodeValue.slice(from, to).trim()) continue;
      try {
        const range = doc.createRange();
        range.setStart(tn.node, from);
        range.setEnd(tn.node, to);
        const mark = doc.createElement('mark');
        mark.className = 'moyue-tts';
        range.surroundContents(mark);
      } catch { /* 跨节点或 DOM 变动时跳过 */ }
    }
    requestAnimationFrame(() => scrollToTTSMark());
  }

  function scrollToTTSMark() {
    const doc = el.frame.contentDocument;
    if (!doc) return;
    const mark = doc.querySelector('mark.moyue-tts');
    if (!mark) return;
    if (isPaginated() && state.page.active) {
      const rect = mark.getBoundingClientRect();
      const x = rect.left + state.page.page * state.page.vw;
      const page = clamp(Math.floor((x + 6) / state.page.vw), 0, state.page.pages - 1);
      if (page !== state.page.page) setPage(page);
      return;
    }
    mark.scrollIntoView({ block: 'center', behavior: 'smooth' });
  }

  async function ttsAudio(text) {
    const t = state.tts;
    const key = `${prefs.ttsVoice}\u0000${text}`;
    if (t.cache.has(key)) return t.cache.get(key);
    const book = state.current;
    if (!book) throw new Error('没有打开的书');
    const res = await fetch(`/api/books/${book.id}/tts`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ chapter: state.chapter, text, voice: prefs.ttsVoice }),
    });
    if (!res.ok) {
      let message = `语音合成失败（${res.status}）`;
      try {
        const data = await res.json();
        if (data && data.error) message = data.error;
      } catch { /* 忽略 */ }
      if (res.status === 401) handleSessionExpired();
      throw new Error(message);
    }
    const blob = await res.blob();
    if (!blob.size) throw new Error('语音服务返回了空音频');
    const url = URL.createObjectURL(blob);
    if (!state.tts.active) {
      URL.revokeObjectURL(url);
      throw new Error('已停止朗读');
    }
    t.cache.set(key, url);
    if (t.cache.size > 24) {
      const oldest = t.cache.keys().next().value;
      const oldURL = t.cache.get(oldest);
      t.cache.delete(oldest);
      URL.revokeObjectURL(oldURL);
    }
    return url;
  }

  function prefetchTTS(index) {
    const t = state.tts;
    if (!t.active || index >= t.sentences.length) return;
    const text = t.sentences[index].text;
    const key = `${prefs.ttsVoice}\u0000${text}`;
    if (t.cache.has(key) || t.prefetching.has(key)) return;
    t.prefetching.add(key);
    ttsAudio(text)
      .catch(() => { /* 预取失败不打扰用户 */ })
      .finally(() => t.prefetching.delete(key));
  }

  function renderTTSBar() {
    const t = state.tts;
    if (!t.active) {
      el.ttsBar.hidden = true;
      return;
    }
    el.ttsBar.hidden = false;
    const total = t.sentences.length;
    const chunk = t.sentences[t.index];
    const partInfo = chunk && chunk.parts.length > 1
      ? ` · 第 ${Math.min(t.part + 1, chunk.parts.length)} / ${chunk.parts.length} 句`
      : '';
    if (t.loading) {
      el.ttsStatus.textContent = tr(`正在合成第 ${Math.min(t.index + 1, total)} / ${total} 段…`);
    } else if (t.playing) {
      el.ttsStatus.textContent = tr(`正在朗读第 ${t.index + 1} / ${total} 段${partInfo}`);
    } else {
      el.ttsStatus.textContent = tr(`已暂停 · 第 ${Math.min(t.index + 1, total)} / ${total} 段${partInfo}`);
    }
    el.ttsPlayIcon.querySelector('use').setAttribute('href', t.playing ? '#i-pause' : '#i-play');
    el.ttsPlay.title = t.playing ? '暂停' : '继续朗读';
    el.ttsPlay.setAttribute('aria-label', el.ttsPlay.title);
  }

  function partRatio(chunk, part) {
    const p = chunk.parts[part];
    if (!p) return 0;
    return clamp(p.charStart / Math.max(1, chunk.text.length), 0, 0.99);
  }

  // 在已加载的音频内跳到某一句（按字数比例估算时间点）。
  function ttsSeekPart(part) {
    const t = state.tts;
    const chunk = t.sentences[t.index];
    if (!chunk) return;
    const p = clamp(part, 0, chunk.parts.length - 1);
    t.part = p;
    highlightTTSSentence(chunk.parts[p]);
    scrollToTTSMark();
    renderTTSBar();
    const audio = t.audio;
    if (audio && Number.isFinite(audio.duration) && audio.duration > 0) {
      t.seeking = true;
      try {
        audio.currentTime = partRatio(chunk, p) * audio.duration;
      } catch {
        t.seeking = false;
      }
    }
  }

  async function speakTTS(index, startPart = 0) {
    const t = state.tts;
    if (!t.active) return;
    if (index >= t.sentences.length) {
      ttsNextChapter();
      return;
    }
    const i = Math.max(0, index);
    const chunk = t.sentences[i];
    const token = ++t.token;
    t.index = i;
    t.part = clamp(startPart, 0, chunk.parts.length - 1);
    t.loading = true;
    t.playing = false;
    t.seeking = false;
    renderTTSBar();
    highlightTTSSentence(chunk.parts[t.part]);
    if (t.audio) {
      try { t.audio.pause(); } catch { /* 忽略 */ }
      t.audio = null;
    }
    try {
      const url = await ttsAudio(chunk.text);
      if (!t.active || token !== t.token) return;
      const audio = new Audio(url);
      audio.playbackRate = clamp(Number(prefs.ttsRate) || 1, 0.5, 2);
      audio.preservesPitch = true;
      t.audio = audio;

      const applyPart = () => {
        if (!Number.isFinite(audio.duration) || audio.duration <= 0) return;
        const p = clamp(t.part, 0, chunk.parts.length - 1);
        if (p <= 0) return;
        t.seeking = true;
        try {
          audio.currentTime = partRatio(chunk, p) * audio.duration;
        } catch {
          t.seeking = false;
        }
      };

      audio.addEventListener('loadedmetadata', applyPart);
      audio.addEventListener('seeked', () => { t.seeking = false; });
      audio.addEventListener('timeupdate', () => {
        if (!t.active || token !== t.token || t.seeking) return;
        if (!Number.isFinite(audio.duration) || audio.duration <= 0 || chunk.parts.length < 2) return;
        const chars = (audio.currentTime / audio.duration) * chunk.text.length;
        let p = chunk.parts.findIndex((part) => chars < part.charEnd);
        if (p < 0) p = chunk.parts.length - 1;
        if (p !== t.part) {
          t.part = p;
          highlightTTSSentence(chunk.parts[p]);
          scrollToTTSMark();
          renderTTSBar();
        }
      });
      audio.addEventListener('ended', () => {
        if (!t.active || token !== t.token) return;
        t.playing = false;
        speakTTS(i + 1, 0);
      });
      audio.addEventListener('error', () => {
        if (!t.active || token !== t.token) return;
        toast('音频播放失败，请检查浏览器是否允许自动播放', { type: 'error' });
        t.loading = false;
        t.playing = false;
        renderTTSBar();
      });

      if (t.part > 0) {
        await new Promise((resolve) => {
          if (audio.readyState >= 1) return resolve();
          audio.addEventListener('loadedmetadata', resolve, { once: true });
          setTimeout(resolve, 1500);
        });
        applyPart();
      }

      await audio.play();
      if (!t.active || token !== t.token) {
        audio.pause();
        return;
      }
      t.loading = false;
      t.playing = true;
      renderTTSBar();
      prefetchTTS(i + 1);
    } catch (err) {
      if (!t.active || token !== t.token) return;
      t.loading = false;
      t.playing = false;
      renderTTSBar();
      toast(err.message || '语音合成失败', { type: 'error' });
    }
  }

  function refreshTTSChapter(fromStart) {
    const t = state.tts;
    if (!t.active) return;
    t.sentences = collectTTSSentences();
    t.chapter = state.chapter;
    if (!t.sentences.length) {
      toast('这一章没有可朗读的文字', { type: 'error' });
      stopTTS();
      return;
    }
    if (fromStart) {
      t.index = 0;
      t.part = 0;
      speakTTS(0, 0);
    } else {
      const i = clamp(t.index, 0, t.sentences.length - 1);
      t.index = i;
      const chunk = t.sentences[i];
      t.part = clamp(t.part, 0, chunk.parts.length - 1);
      highlightTTSSentence(chunk.parts[t.part]);
      renderTTSBar();
    }
  }

  function ttsNextChapter() {
    const book = state.current;
    if (!book) return stopTTS();
    if (state.chapter >= book.chapters.length - 1) {
      toast('已朗读到本书结尾');
      stopTTS();
      return;
    }
    loadChapter(state.chapter + 1, 0);
  }

  function setTTSLayout(on) {
    const text = isTextBook(state.current);
    el.reader.classList.toggle('tts-on', !!on && text);
    if (text && isPaginated() && state.page.active) {
      paginateText(state.fraction);
    }
  }

  // 计算当前屏幕位置对应的正文偏移：分页模式取当前页左上角，
  // 滚动模式取视口顶部，这样从「看到哪里」开始朗读，而不是章首。
  function caretOffsetAt(doc, x, y) {
    let range = null;
    if (typeof doc.caretRangeFromPoint === 'function') {
      range = doc.caretRangeFromPoint(x, y);
    } else if (typeof doc.caretPositionFromPoint === 'function') {
      const pos = doc.caretPositionFromPoint(x, y);
      if (pos) {
        range = doc.createRange();
        range.setStart(pos.offsetNode, pos.offset);
        range.collapse(true);
      }
    }
    if (!range || !range.startContainer || !doc.body.contains(range.startContainer)) return null;
    try {
      const pre = doc.createRange();
      pre.selectNodeContents(doc.body);
      pre.setEnd(range.startContainer, range.startOffset);
      return pre.toString().length;
    } catch {
      return null;
    }
  }

  function ttsStartOffset() {
    const doc = el.frame.contentDocument;
    const win = el.frame.contentWindow;
    if (!doc || !doc.body || !win) return 0;
    const x = isPaginated() && state.page.active ? state.page.vw * 0.25 : win.innerWidth * 0.5;
    const y = 40;
    let offset = caretOffsetAt(doc, x, y);
    if (offset == null) offset = 0;
    // 页面顶部可能是页边距或图片，caret 落不到文字时退回到第一个可见文本节点。
    if (offset === 0) {
      const vw = win.innerWidth;
      const vh = win.innerHeight;
      let best = null;
      for (const tn of textNodes(doc)) {
        if (!tn.node.nodeValue || !tn.node.nodeValue.trim()) continue;
        let rects;
        try {
          const range = doc.createRange();
          range.selectNodeContents(tn.node);
          rects = range.getClientRects();
        } catch {
          continue;
        }
        for (const rect of rects) {
          if (rect.bottom <= 2 || rect.top >= vh - 2 || rect.right <= 0 || rect.left >= vw) continue;
          if (!best || rect.top < best.top - 1 || (Math.abs(rect.top - best.top) <= 1 && rect.left < best.left)) {
            best = { top: rect.top, left: rect.left, start: tn.start };
          }
        }
      }
      if (best) offset = best.start;
    }
    return offset;
  }

  function findTTSStartPosition(chunks, offset) {
    if (!chunks.length) return { index: 0, part: 0 };
    for (let i = 0; i < chunks.length; i++) {
      const parts = chunks[i].parts;
      for (let j = 0; j < parts.length; j++) {
        if (parts[j].end > offset) return { index: i, part: j };
      }
    }
    return { index: chunks.length - 1, part: chunks[chunks.length - 1].parts.length - 1 };
  }

  async function startTTS() {
    if (!isTextBook(state.current)) {
      toast('当前格式暂不支持语音朗读', { type: 'error' });
      return;
    }
    if (!state.tts.status) await loadTTSStatus();
    if (!ttsEnabled()) {
      toast('管理员还没有配置语音朗读服务', { type: 'error' });
      return;
    }
    const sentences = collectTTSSentences();
    if (!sentences.length) {
      toast('当前章节没有可朗读的文字', { type: 'error' });
      return;
    }
    const start = findTTSStartPosition(sentences, ttsStartOffset());
    state.tts.active = true;
    state.tts.sentences = sentences;
    state.tts.chapter = state.chapter;
    state.tts.index = start.index;
    state.tts.part = start.part;
    el.ttsBtn.classList.add('is-on');
    fillTTSVoices();
    setTTSLayout(true);
    renderTTSBar();
    speakTTS(start.index, start.part);
  }

  function stopTTS() {
    const t = state.tts;
    t.active = false;
    t.playing = false;
    t.loading = false;
    t.token++;
    if (t.audio) {
      try { t.audio.pause(); } catch { /* 忽略 */ }
      t.audio = null;
    }
    t.cache.forEach((url) => URL.revokeObjectURL(url));
    t.cache.clear();
    t.prefetching.clear();
    t.sentences = [];
    t.index = 0;
    t.part = 0;
    t.seeking = false;
    clearTTSHighlight();
    el.ttsBar.hidden = true;
    el.ttsBtn.classList.remove('is-on');
    setTTSLayout(false);
  }

  function toggleTTS() {
    if (state.tts.active) stopTTS();
    else startTTS();
  }

  function toggleTTSPlay() {
    const t = state.tts;
    if (!t.active) {
      startTTS();
      return;
    }
    if (t.loading) return;
    if (t.audio && !t.audio.paused) {
      t.audio.pause();
      t.playing = false;
      renderTTSBar();
      return;
    }
    if (t.audio) {
      t.audio.playbackRate = clamp(Number(prefs.ttsRate) || 1, 0.5, 2);
      t.audio.play().then(() => {
        t.playing = true;
        renderTTSBar();
      }).catch(() => speakTTS(t.index));
      return;
    }
    speakTTS(t.index);
  }

  el.ttsBtn.addEventListener('click', toggleTTS);
  el.ttsPlay.addEventListener('click', toggleTTSPlay);
  el.ttsClose.addEventListener('click', stopTTS);
  el.ttsPrev.addEventListener('click', () => {
    const t = state.tts;
    if (!t.active) return;
    const chunk = t.sentences[t.index];
    if (!chunk) return;
    if (t.part > 0) {
      ttsSeekPart(t.part - 1);
    } else if (t.index > 0) {
      const prev = t.sentences[t.index - 1];
      speakTTS(t.index - 1, prev.parts.length - 1);
    } else {
      ttsSeekPart(0);
    }
  });
  el.ttsNext.addEventListener('click', () => {
    const t = state.tts;
    if (!t.active) return;
    const chunk = t.sentences[t.index];
    if (!chunk) return;
    if (t.part < chunk.parts.length - 1) {
      ttsSeekPart(t.part + 1);
    } else {
      speakTTS(t.index + 1, 0);
    }
  });
  el.ttsVoice.addEventListener('change', () => {
    prefs.ttsVoice = el.ttsVoice.value;
    savePrefs();
    if (state.tts.active) speakTTS(state.tts.index, state.tts.part);
  });
  el.ttsRate.addEventListener('change', () => {
    prefs.ttsRate = Number(el.ttsRate.value) || 1;
    savePrefs();
    if (state.tts.audio) state.tts.audio.playbackRate = prefs.ttsRate;
  });

  /* ============================ 上传 ============================ */

  const UPLOAD_EXT = /\.(epub|txt|pdf|cbz|cbr|fb2|mobi|azw3?|prc|mp3|m4a|m4b|flac|ogg|opus|wav)$/i;

  function fmtBytes(bytes) {
    const n = Number(bytes) || 0;
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
    return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
  }

  function fillUploadLibraries() {
    const libs = (state.libraries || []).filter((l) => l.kind !== 'webdav');
    const current = state.selectedLibrary !== 'all' && libs.some((l) => l.id === state.selectedLibrary)
      ? state.selectedLibrary
      : (libs[0]?.id || '');
    el.uploadLib.innerHTML = libs.length
      ? libs.map((l) => `<option value="${l.id}">${esc(l.name)}</option>`).join('')
      : '<option value="">默认书库</option>';
    el.uploadLib.value = current;
  }

  function openUploadDialog(files) {
    if (!state.user?.isAdmin) {
      toast('只有管理员可以导入书籍', { type: 'error' });
      return;
    }
    fillUploadLibraries();
    if (!state.upload.running && state.upload.items.length
      && state.upload.items.every((i) => i.status === 'done' || i.status === 'duplicate')) {
      state.upload.items = [];
    }
    el.uploadScrim.hidden = false;
    if (files && files.length) addUploadFiles(files);
    else renderUploadList();
    setTimeout(() => el.uploadPick.focus?.(), 80);
  }

  function closeUploadDialog() {
    if (state.upload.running) {
      cancelUploads();
    }
    el.uploadScrim.hidden = true;
    el.dropOverlay.hidden = true;
  }

  function uploadStatusText(item) {
    switch (item.status) {
      case 'uploading': return tr(`正在上传 ${Math.round(item.progress * 100)}%`);
      case 'parsing': return tr('校验并解析文件…');
      case 'done': return item.title ? tr(`已导入《${item.title}》`) : tr('已导入');
      case 'duplicate': return item.title ? tr(`已在书架中：《${item.title}》`) : tr('已在书架中');
      case 'error': return tr(item.error || '上传失败');
      default: return tr('等待上传');
    }
  }

  function uploadItemClass(item) {
    if (item.status === 'done' || item.status === 'duplicate') return ' is-done';
    if (item.status === 'error') return ' is-error';
    return '';
  }

  function renderUploadItem(item) {
    const row = el.uploadList.querySelector(`[data-uid="${item.id}"]`);
    if (!row) return renderUploadList();
    row.className = 'upload-item' + uploadItemClass(item);
    row.querySelector('.upload-item-sub').textContent = `${fmtBytes(item.file.size)} · ${uploadStatusText(item)}`;
    row.querySelector('.upload-item-bar i').style.width = `${Math.round(item.progress * 100)}%`;
    row.querySelector('.upload-item-pct').textContent = `${Math.round(item.progress * 100)}%`;
    row.querySelector('.upload-item-remove').hidden = state.upload.running || item.status === 'uploading';
    updateUploadSummary();
  }

  function renderUploadList() {
    el.uploadList.innerHTML = state.upload.items.map((item) => `
      <div class="upload-item${uploadItemClass(item)}" data-uid="${item.id}">
        <div class="upload-item-icon"><svg class="icon"><use href="#i-file"/></svg></div>
        <div class="upload-item-main">
          <div class="upload-item-name" title="${esc(item.file.name)}">${esc(item.file.name)}</div>
          <div class="upload-item-sub">${esc(fmtBytes(item.file.size))} · ${esc(uploadStatusText(item))}</div>
          <div class="upload-item-bar"><i style="width:${Math.round(item.progress * 100)}%"></i></div>
        </div>
        <div class="upload-item-pct">${Math.round(item.progress * 100)}%</div>
        <button class="upload-item-remove" data-remove="${item.id}" aria-label="移除" ${state.upload.running ? 'hidden' : ''}>
          <svg class="icon"><use href="#i-close"/></svg>
        </button>
      </div>`).join('');
    updateUploadSummary();
  }

  function updateUploadSummary() {
    const items = state.upload.items;
    const totalBytes = items.reduce((sum, i) => sum + (i.file.size || 0), 0);
    const doneBytes = items.reduce((sum, i) => sum + (i.file.size || 0) * (i.status === 'parsing' ? 0.98 : i.progress), 0);
    const percent = totalBytes > 0 ? clamp(doneBytes / totalBytes * 100, 0, 100) : 0;
    const done = items.filter((i) => i.status === 'done' || i.status === 'duplicate').length;
    const failed = items.filter((i) => i.status === 'error').length;

    el.uploadTotalBar.style.width = `${percent}%`;
    el.uploadPercent.textContent = items.length ? `${Math.round(percent)}%` : '';
    if (!items.length) {
      el.uploadSummary.textContent = tr('还没有选择文件');
    } else if (state.upload.running) {
      const active = items.find((i) => i.status === 'parsing' || i.status === 'uploading');
      el.uploadSummary.textContent = active && active.status === 'parsing'
        ? tr(`正在解析《${active.file.name}》…`)
        : tr(`正在导入 ${done} / ${items.length} 个文件`);
    } else if (done + failed === items.length) {
      el.uploadSummary.textContent = failed
        ? tr(`完成 ${done} 个 · 失败 ${failed} 个`)
        : tr(`已完成 ${done} 个文件`);
    } else {
      el.uploadSummary.textContent = tr(`已选择 ${items.length} 个文件 · ${fmtBytes(totalBytes)}`);
    }

    const pending = items.some((i) => i.status === 'pending' || i.status === 'error');
    el.uploadStart.hidden = !pending && !state.upload.running;
    el.uploadStart.disabled = state.upload.running || !pending;
    el.uploadStart.textContent = state.upload.running
      ? '正在上传…'
      : pending && items.some((i) => i.status === 'error') ? '重试失败项' : '开始上传';
    el.uploadCancel.textContent = state.upload.running
      ? '停止上传'
      : items.length && done + failed === items.length ? '关闭' : '取消';
  }

  function addUploadFiles(fileList) {
    if (state.upload.running) return;
    const incoming = Array.from(fileList || []);
    if (!incoming.length) return;
    let rejected = 0;
    let duplicated = 0;
    for (const file of incoming) {
      if (!UPLOAD_EXT.test(file.name)) {
        rejected++;
        continue;
      }
      if (file.size > 200 * 1024 * 1024) {
        rejected++;
        continue;
      }
      if (state.upload.items.some((i) => i.file.name === file.name && i.file.size === file.size)) {
        duplicated++;
        continue;
      }
      state.upload.items.push({
        id: String(state.upload.seq++),
        file,
        status: 'pending',
        progress: 0,
        error: '',
        title: '',
      });
    }
    if (rejected) toast('已跳过不支持或超过 200 MB 的文件', { type: 'error' });
    if (duplicated) toast(`已跳过 ${duplicated} 个重复文件`);
    renderUploadList();
  }

  function removeUploadItem(id) {
    if (state.upload.running) return;
    state.upload.items = state.upload.items.filter((i) => i.id !== id);
    renderUploadList();
  }

  function uploadOne(item) {
    return new Promise((resolve) => {
      const xhr = new XMLHttpRequest();
      state.upload.xhr = xhr;
      xhr.open('POST', '/api/books');
      xhr.withCredentials = true;

      xhr.upload.addEventListener('progress', (event) => {
        if (!event.lengthComputable || !event.total) return;
        item.progress = Math.min(0.96, event.loaded / event.total);
        renderUploadItem(item);
      });
      xhr.upload.addEventListener('load', () => {
        item.status = 'parsing';
        item.progress = 0.98;
        renderUploadItem(item);
      });
      xhr.addEventListener('load', () => {
        state.upload.xhr = null;
        let data = {};
        try { data = JSON.parse(xhr.responseText || '{}'); } catch { /* 忽略 */ }
        if (xhr.status === 401) {
          item.status = 'error';
          item.error = '登录已过期';
          resolve();
          handleSessionExpired();
          return;
        }
        if (xhr.status >= 200 && xhr.status < 300 && data.book) {
          item.title = data.book.title || '';
          item.progress = 1;
          item.status = data.duplicated ? 'duplicate' : 'done';
        } else {
          item.status = 'error';
          item.progress = 0;
          item.error = data.error || `上传失败（${xhr.status}）`;
        }
        renderUploadItem(item);
        resolve();
      });
      xhr.addEventListener('error', () => {
        state.upload.xhr = null;
        item.status = 'error';
        item.progress = 0;
        item.error = '网络中断，请重试';
        renderUploadItem(item);
        resolve();
      });
      xhr.addEventListener('abort', () => {
        state.upload.xhr = null;
        if (item.status === 'uploading' || item.status === 'parsing') {
          item.status = 'pending';
          item.progress = 0;
          item.error = '';
        }
        renderUploadItem(item);
        resolve();
      });

      const form = new FormData();
      form.append('file', item.file);
      const lib = el.uploadLib.value;
      if (lib) form.append('libraryId', lib);
      xhr.send(form);
    });
  }

  async function startUploads() {
    if (state.upload.running) return;
    const queue = state.upload.items.filter((i) => i.status === 'pending' || i.status === 'error');
    if (!queue.length) return;
    state.upload.running = true;
    state.upload.cancelled = false;
    updateUploadSummary();
    renderUploadList();

    let added = 0;
    for (const item of queue) {
      if (state.upload.cancelled) break;
      item.status = 'uploading';
      item.progress = 0;
      item.error = '';
      renderUploadItem(item);
      await uploadOne(item);
      if (item.status === 'done') added++;
    }

    state.upload.running = false;
    state.upload.xhr = null;
    renderUploadList();
    if (added > 0) {
      await loadLibrary({ silent: true });
      if (state.view === 'admin') loadAdmin();
    }
    if (!state.upload.cancelled) {
      const failed = state.upload.items.filter((i) => i.status === 'error').length;
      if (failed === 0) toast(`已导入 ${added} 本书`);
      else toast(`${failed} 个文件导入失败，可重试`, { type: 'error' });
    }
  }

  function cancelUploads() {
    state.upload.cancelled = true;
    if (state.upload.xhr) {
      try { state.upload.xhr.abort(); } catch { /* 忽略 */ }
    }
    state.upload.running = false;
    state.upload.xhr = null;
    renderUploadList();
  }

  /* ============================ 设置 ============================ */

  function applyReadingLayout() {
    const book = state.current;
    if (!book || state.view !== 'reader') return;
    if (isTextBook(book)) {
      const doc = el.frame.contentDocument;
      if (!doc || !doc.body) return;
      applyFrameStyle();
      if (prefs.mode === 'scroll') {
        clearPagination();
        restoreScroll(state.fraction);
      } else {
        paginateText(state.fraction);
      }
      if (state.tts.active) refreshTTSChapter(false);
      updateChrome();
      updateProgressUI();
      return;
    }
    const want = prefs.mode === 'spread' && isSpreadWide();
    const has = book.type === 'pdf' ? state.pdf.spread : state.comicSpread;
    if (want !== has) {
      if (book.type === 'pdf') {
        state.pdf.ready = false;
        state.pdf.loading = false;
      }
      loadChapter(state.chapter, state.fraction);
    }
    updateChrome();
    updateProgressUI();
  }

  function applyPrefs({ frame = true } = {}) {
    applyAppTheme();
    savePrefs();
    syncSettingsUI();
    if (frame && state.view === 'reader') applyReadingLayout();
  }

  el.modeSeg.addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-mode]');
    if (!btn) return;
    prefs.mode = btn.dataset.mode;
    applyPrefs();
    if (prefs.mode === 'spread' && !isSpreadWide()) {
      toast('窗口较窄，双页会以单页显示', { duration: 2600 });
    }
  });

  el.themeGrid.addEventListener('click', (e) => {
    const chip = e.target.closest('.theme-chip');
    if (!chip) return;
    prefs.theme = chip.dataset.theme;
    applyPrefs();
  });

  el.fontSeg.addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-font]');
    if (!btn) return;
    prefs.font = btn.dataset.font;
    applyPrefs();
  });

  el.sizeRange.addEventListener('input', () => {
    prefs.size = Number(el.sizeRange.value);
    applyPrefs();
  });
  el.sizeUp.addEventListener('click', () => {
    prefs.size = clamp(prefs.size + 1, 14, 30);
    applyPrefs();
  });
  el.sizeDown.addEventListener('click', () => {
    prefs.size = clamp(prefs.size - 1, 14, 30);
    applyPrefs();
  });
  el.lineRange.addEventListener('input', () => {
    prefs.line = Number(el.lineRange.value) / 10;
    applyPrefs();
  });
  el.measureRange.addEventListener('input', () => {
    prefs.measure = Number(el.measureRange.value);
    applyPrefs();
  });
  el.resetPrefs.addEventListener('click', () => {
    Object.assign(prefs, DEFAULT_PREFS);
    applyPrefs();
    applyLang();
    toast('已恢复默认阅读设置');
  });

  el.authLang.addEventListener('change', () => {
    prefs.lang = normalizeLang(el.authLang.value);
    savePrefs();
    applyLang();
  });
  el.langSeg.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-lang]');
    if (!btn) return;
    prefs.lang = normalizeLang(btn.dataset.lang);
    savePrefs();
    applyLang();
  });

  /* ============================ 书架事件 ============================ */

  el.authForm.addEventListener('submit', submitAuth);

  el.librarySwitch.addEventListener('click', (e) => {
    const chip = e.target.closest('[data-lib]');
    if (!chip) return;
    state.selectedLibrary = chip.dataset.lib;
    state.folder = '';
    renderLibrarySwitch();
    renderContinue();
    renderShelf();
  });

  el.uploadBtn.addEventListener('click', () => {
    if (!state.user?.isAdmin) return;
    openUploadDialog();
  });
  el.emptyUpload.addEventListener('click', () => {
    if (!state.user?.isAdmin) return;
    openUploadDialog();
  });

  el.uploadClose.addEventListener('click', closeUploadDialog);
  el.uploadCancel.addEventListener('click', () => {
    if (state.upload.running) {
      cancelUploads();
      return;
    }
    closeUploadDialog();
  });
  el.uploadScrim.addEventListener('click', (e) => {
    if (e.target === el.uploadScrim) closeUploadDialog();
  });
  el.uploadPick.addEventListener('change', () => {
    addUploadFiles(el.uploadPick.files);
    el.uploadPick.value = '';
  });
  el.uploadList.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-remove]');
    if (btn) removeUploadItem(btn.dataset.remove);
  });
  el.uploadStart.addEventListener('click', startUploads);

  let dragDepth = 0;
  let internalDrag = false;

  el.uploadScrim.addEventListener('dragover', (e) => {
    if (internalDrag) return;
    e.preventDefault();
    el.uploadDrop.classList.add('is-over');
  });
  el.uploadScrim.addEventListener('dragleave', (e) => {
    if (!el.uploadScrim.contains(e.relatedTarget)) el.uploadDrop.classList.remove('is-over');
  });
  el.uploadScrim.addEventListener('drop', (e) => {
    e.preventDefault();
    e.stopPropagation();
    el.uploadDrop.classList.remove('is-over');
    if (internalDrag) { internalDrag = false; return; }
    if (!state.upload.running) addUploadFiles(e.dataTransfer.files);
  });

  el.folderTrail.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-folder-nav]');
    if (!btn) return;
    openFolder(btn.dataset.folderNav || '');
  });

  el.search.addEventListener('input', () => {
    state.query = el.search.value;
    renderShelf();
  });

  el.sort.addEventListener('change', () => {
    state.sort = el.sort.value;
    renderShelf();
  });

  el.grid.addEventListener('click', async (e) => {
    const folder = e.target.closest('[data-folder]');
    if (folder) {
      openFolder(folder.dataset.folder);
      return;
    }

    const menuBtn = e.target.closest('[data-menu]');
    if (menuBtn) {
      e.stopPropagation();
      const panel = el.grid.querySelector(`[data-menu-panel="${CSS.escape(menuBtn.dataset.menu)}"]`);
      const willOpen = panel && !panel.classList.contains('open');
      closeMenus();
      if (willOpen) {
        panel.classList.add('open');
        menuBtn.closest('.book')?.classList.add('menu-open');
      }
      return;
    }

    const action = e.target.closest('[data-act]');
    if (action) {
      e.stopPropagation();
      closeMenus();
      const id = action.dataset.id;
      const book = state.books.find((b) => b.id === id);
      if (!book) return;
      if (action.dataset.act === 'open') {
        openBook(id);
      } else if (action.dataset.act === 'download') {
        window.location.href = `/api/books/${id}/file`;
      } else if (action.dataset.act === 'reset') {
        const ok = await confirmDialog({
          title: '标记为未读？',
          text: `将清除《${book.title}》的阅读进度，阅读时长与书签会保留。`,
          okText: '标记未读',
          danger: false,
        });
        if (!ok) return;
        try {
          await api.resetProgress(id);
          toast('已标记为未读');
          loadLibrary({ silent: true });
        } catch (err) {
          if (err.status !== 401) toast(err.message, { type: 'error' });
        }
      } else if (action.dataset.act === 'delete') {
        const ok = await confirmDialog({
          title: '从书库删除？',
          text: `《${book.title}》的记录与服务器上的文件都会被删除，所有用户的阅读记录也会一并清除。`,
          okText: '删除',
        });
        if (!ok) return;
        try {
          await api.remove(id);
          toast('已删除');
          loadLibrary({ silent: true });
        } catch (err) {
          if (err.status !== 401) toast(err.message, { type: 'error' });
        }
      }
      return;
    }

    const opener = e.target.closest('[data-open]');
    if (opener) openBook(opener.dataset.open);
  });

  el.grid.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    const folder = e.target.closest('[data-folder]');
    if (folder) {
      e.preventDefault();
      openFolder(folder.dataset.folder);
      return;
    }
    const opener = e.target.closest('[data-open]');
    if (!opener) return;
    e.preventDefault();
    openBook(opener.dataset.open);
  });

  el.continueCard.addEventListener('click', (e) => {
    const opener = e.target.closest('[data-open]');
    if (opener) openBook(opener.dataset.open);
  });

  document.addEventListener('click', (e) => {
    if (!e.target.closest('.book')) closeMenus();
    if (!e.target.closest('#userMenu')) {
      el.userPop.hidden = true;
      el.avatarBtn.setAttribute('aria-expanded', 'false');
    }
  });

  el.avatarBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    const open = el.userPop.hidden;
    el.userPop.hidden = !open;
    el.avatarBtn.setAttribute('aria-expanded', String(open));
  });

  el.userTheme.addEventListener('change', () => {
    prefs.theme = el.userTheme.value;
    applyPrefs();
  });

  el.userLang.addEventListener('change', () => {
    prefs.lang = normalizeLang(el.userLang.value);
    savePrefs();
    applyLang();
  });

  el.userPop.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-user-act]');
    if (!btn) return;
    const act = btn.dataset.userAct;
    el.userPop.hidden = true;
    el.avatarBtn.setAttribute('aria-expanded', 'false');
    if (act === 'logout') {
      logout();
    } else if (act === 'admin') {
      openAdmin();
    } else if (act === 'sessions') {
      openSessions();
    } else if (act === 'install') {
      installApp();
    } else if (act === 'password') {
      el.passwordScrim.hidden = false;
      el.passwordForm.reset();
      setTimeout(() => el.pwCurrent.focus(), 50);
    }
  });

  /* ============================ 密码 ============================ */

  el.passwordForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const current = el.pwCurrent.value;
    const next = el.pwNew.value;
    if (next.length < 6) return toast('新密码至少需要 6 个字符', { type: 'error' });
    if (next !== el.pwConfirm.value) return toast('两次输入的新密码不一致', { type: 'error' });
    try {
      await api.changePassword({ currentPassword: current, newPassword: next });
      el.passwordScrim.hidden = true;
      el.passwordForm.reset();
      toast('密码已修改');
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    }
  });
  el.pwCancel.addEventListener('click', () => {
    el.passwordScrim.hidden = true;
    el.passwordForm.reset();
  });
  el.passwordScrim.addEventListener('click', (e) => {
    if (e.target === el.passwordScrim) el.passwordScrim.hidden = true;
  });

  /* ============================ 管理台事件 ============================ */

  el.adminBack.addEventListener('click', () => {
    history.pushState(null, '', location.pathname);
    enterLibrary({ updateHistory: false });
  });

  $$('.admin-tab').forEach((tab) => {
    tab.addEventListener('click', () => {
      state.admin.tab = tab.dataset.atab;
      $$('.admin-tab').forEach((t) => t.classList.toggle('is-active', t === tab));
      el.adminLibsSection.hidden = state.admin.tab !== 'libs';
      el.adminUsersSection.hidden = state.admin.tab !== 'users';
      el.adminSettingsSection.hidden = state.admin.tab !== 'settings';
    });
  });

  function syncLibKindFields() {
    if (!el.libKind) return;
    const dav = el.libKind.value === 'webdav';
    el.libPathField.hidden = dav;
    el.libDavUrlField.hidden = !dav;
    el.libDavUserField.hidden = !dav;
    el.libDavPassField.hidden = !dav;
    const pathInput = el.libPathField.querySelector('input');
    const urlInput = el.libDavUrlField.querySelector('input');
    if (pathInput) pathInput.required = !dav;
    if (urlInput) urlInput.required = dav;
  }

  el.libKind.addEventListener('change', syncLibKindFields);

  el.libForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const fd = new FormData(el.libForm);
    const kind = String(fd.get('kind') || 'local');
    const btn = el.libForm.querySelector('button[type="submit"]');
    btn.disabled = true;
    try {
      await api.createLibrary({
        name: String(fd.get('name') || '').trim(),
        kind,
        path: kind === 'local' ? String(fd.get('path') || '').trim() : '',
        type: String(fd.get('type') || 'mixed'),
        url: kind === 'webdav' ? String(fd.get('url') || '').trim() : '',
        username: kind === 'webdav' ? String(fd.get('username') || '').trim() : '',
        password: kind === 'webdav' ? String(fd.get('password') || '') : '',
      });
      el.libForm.reset();
      syncLibKindFields();
      toast('书库已添加，正在扫描');
      await loadAdmin();
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    } finally {
      btn.disabled = false;
    }
  });

  el.libList.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-lib-act]');
    if (!btn) return;
    const id = btn.dataset.id;
    const lib = state.admin.libraries.find((l) => l.id === id);
    if (!lib) return;
    if (btn.dataset.libAct === 'scan') {
      btn.disabled = true;
      try {
        await api.scanLibrary(id);
        toast('开始扫描「' + lib.name + '」');
        await loadAdmin();
      } catch (err) {
        if (err.status !== 401) toast(err.message, { type: 'error' });
      }
    } else if (btn.dataset.libAct === 'delete') {
      const ok = await confirmDialog({
        title: '删除书库？',
        text: `将从墨阅移除「${lib.name}」及其 ${lib.bookCount || 0} 本书的记录。服务器上的文件不会被删除。`,
        okText: '删除书库',
      });
      if (!ok) return;
      try {
        await api.deleteLibrary(id);
        toast('书库已删除');
        await loadAdmin();
      } catch (err) {
        if (err.status !== 401) toast(err.message, { type: 'error' });
      }
    }
  });

  el.refreshLibs.addEventListener('click', () => loadAdmin());
  el.refreshUsers.addEventListener('click', () => loadAdmin());

  el.userForm.addEventListener('submit', submitUserForm);
  el.userCancelEdit.addEventListener('click', resetUserForm);

  el.userList.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-user-act]');
    if (!btn) return;
    const id = btn.dataset.id;
    const u = state.admin.users.find((x) => x.id === id);
    if (!u) return;
    if (btn.dataset.userAct === 'edit') {
      startUserEdit(id);
    } else if (btn.dataset.userAct === 'delete') {
      const ok = await confirmDialog({
        title: '删除账号？',
        text: `账号「${u.displayName || u.username}」及其阅读记录、书签会被永久删除。`,
        okText: '删除账号',
      });
      if (!ok) return;
      try {
        await api.deleteUser(id);
        toast('账号已删除');
        await loadAdmin();
      } catch (err) {
        if (err.status !== 401) toast(err.message, { type: 'error' });
      }
    }
  });

  /* ============================ 阅读器事件 ============================ */

  el.backBtn.addEventListener('click', () => exitReader());
  el.tocBtn.addEventListener('click', () => openDrawer('toc', 'toc'));
  el.settingsBtn.addEventListener('click', () => openDrawer('settings'));
  el.markBtn.addEventListener('click', toggleBookmark);
  el.fullBtn.addEventListener('click', () => toggleFullscreen());
  el.scrim.addEventListener('click', closeDrawers);
  el.prevBtn.addEventListener('click', () => advance(-1));
  el.nextBtn.addEventListener('click', () => advance(1));
  el.nextHint.addEventListener('click', () => advance(1));

  $$('[data-close-drawer]').forEach((btn) => btn.addEventListener('click', closeDrawers));

  el.searchGo.addEventListener('click', runSearch);
  el.searchInputInBook.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      runSearch();
    }
  });

  el.annoBar.addEventListener('click', (e) => {
    const color = e.target.closest('.anno-color');
    if (color) {
      saveHighlight(color.dataset.color);
      return;
    }
    if (e.target.closest('#annoNote')) {
      openNoteDialog();
      return;
    }
    if (e.target.closest('#annoCancel')) {
      hideAnnoBar();
      clearFrameSelection();
    }
  });

  el.noteColors.addEventListener('click', (e) => {
    const color = e.target.closest('.anno-color');
    if (!color) return;
    state.noteColor = color.dataset.color;
    el.noteColors.querySelectorAll('.anno-color').forEach((c) =>
      c.classList.toggle('is-active', c === color));
  });
  el.noteSave.addEventListener('click', saveNote);
  el.noteCancel.addEventListener('click', () => {
    const wasNew = !state.noteEditing;
    closeNoteDialog();
    if (wasNew) {
      hideAnnoBar();
      clearFrameSelection();
    }
  });
  el.noteScrim.addEventListener('click', (e) => {
    if (e.target !== el.noteScrim) return;
    const wasNew = !state.noteEditing;
    closeNoteDialog();
    if (wasNew) {
      hideAnnoBar();
      clearFrameSelection();
    }
  });
  el.noteDelete.addEventListener('click', () => {
    if (state.noteEditing) deleteAnnotation(state.noteEditing.id);
  });

  el.sessionClose.addEventListener('click', () => { el.sessionScrim.hidden = true; });
  el.sessionScrim.addEventListener('click', (e) => {
    if (e.target === el.sessionScrim) el.sessionScrim.hidden = true;
  });
  el.sessionRevokeOthers.addEventListener('click', async () => {
    const ok = await confirmDialog({
      title: '退出其它设备？',
      text: '除当前设备外，其它登录会话都会立即失效，需要重新输入密码。',
      okText: '退出其它设备',
    });
    if (!ok) return;
    try {
      await api.revokeOtherSessions();
      toast('已退出其它设备');
      await loadSessions();
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    }
  });
  el.sessionList.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-revoke]');
    if (!btn) return;
    try {
      const res = await api.revokeSession(btn.dataset.revoke);
      if (res.current) {
        el.sessionScrim.hidden = true;
        handleSessionExpired();
        return;
      }
      toast('已撤销该设备');
      await loadSessions();
    } catch (err) {
      if (err.status !== 401) toast(err.message, { type: 'error' });
    }
  });

  el.tocDrawer.addEventListener('click', async (e) => {
    const tab = e.target.closest('.tab');
    if (tab) {
      switchTab(tab.dataset.tab);
      return;
    }
    const btn = e.target.closest('[data-chapter]');
    if (btn && !btn.disabled && !btn.closest('.search-hit')) {
      const index = Number(btn.dataset.chapter);
      if (index >= 0) {
        loadChapter(index, 0);
        if (window.innerWidth <= 860) closeDrawers();
      }
      return;
    }
    const noteEdit = e.target.closest('[data-note-edit]');
    if (noteEdit) {
      const a = (state.current?.annotations || []).find((x) => x.id === noteEdit.dataset.noteEdit);
      if (a) openNoteDialog(a);
      return;
    }
    const noteDel = e.target.closest('[data-note-del]');
    if (noteDel) {
      deleteAnnotation(noteDel.dataset.noteDel);
      return;
    }
    const noteItem = e.target.closest('[data-note]');
    if (noteItem) {
      const a = (state.current?.annotations || []).find((x) => x.id === noteItem.dataset.note);
      if (a) jumpToAnnotation(a);
      return;
    }
    const hit = e.target.closest('.search-hit');
    if (hit) {
      jumpToSearchHit(hit);
      if (window.innerWidth <= 860) closeDrawers();
      return;
    }
    const del = e.target.closest('[data-del]');
    if (del) {
      e.stopPropagation();
      const id = state.current?.id;
      if (!id) return;
      try {
        await api.removeBookmark(id, del.dataset.del);
        state.current.bookmarks = (state.current.bookmarks || []).filter((m) => m.id !== del.dataset.del);
        renderBookmarks();
        updateBookmarkState();
        toast('已删除书签');
      } catch (err) {
        if (err.status !== 401) toast(err.message, { type: 'error' });
      }
      return;
    }
    const mark = e.target.closest('[data-jump]');
    if (mark) {
      loadChapter(Number(mark.dataset.jump), Number(mark.dataset.fraction) || 0);
      if (window.innerWidth <= 860) closeDrawers();
    }
  });

  el.range.addEventListener('input', () => {
    const percent = Number(el.range.value) / 10;
    setFill(el.range, percent);
    el.progressLabel.textContent = `${Math.round(percent)}%`;
  });
  el.range.addEventListener('change', () => jumpTo(Number(el.range.value)));

  el.frame.addEventListener('load', onFrameLoad);

  let resizeTimer = 0;
  window.addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      if (state.view === 'reader') applyReadingLayout();
    }, 160);
  });

  window.addEventListener('scroll', () => {
    el.topbar.classList.toggle('is-scrolled', window.scrollY > 8);
  }, { passive: true });

  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden' && state.view === 'reader') {
      flushProgress();
      flushReadingTime();
    }
  });

  window.addEventListener('beforeunload', () => {
    if (state.view === 'reader') {
      flushProgress();
      flushReadingTime();
    }
  });

  /* 拖拽导入 */
  // 页面内拖动图片、封面或选中文字时，浏览器也会在 dataTransfer 里带上 Files。
  // 只有从系统拖入文件，并且停留在书架或管理台时才接收，避免阅读时误弹上传框。
  function canAcceptFileDrop(e) {
    if (internalDrag) return false;
    if (!state.user?.isAdmin) return false;
    if (state.view !== 'library' && state.view !== 'admin') return false;
    if (!el.uploadScrim.hidden) return false;
    if (!e.dataTransfer) return false;
    return Array.from(e.dataTransfer.types || []).includes('Files');
  }

  window.addEventListener('dragstart', () => { internalDrag = true; }, true);
  window.addEventListener('dragend', () => {
    internalDrag = false;
    dragDepth = 0;
    el.dropOverlay.hidden = true;
  }, true);

  window.addEventListener('dragenter', (e) => {
    if (!canAcceptFileDrop(e)) return;
    dragDepth++;
    el.dropOverlay.hidden = false;
  });
  window.addEventListener('dragover', (e) => {
    if (el.dropOverlay.hidden) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
  });
  window.addEventListener('dragleave', () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) el.dropOverlay.hidden = true;
  });
  window.addEventListener('drop', (e) => {
    if (el.dropOverlay.hidden) return;
    e.preventDefault();
    dragDepth = 0;
    el.dropOverlay.hidden = true;
    if (!internalDrag) openUploadDialog(e.dataTransfer.files);
    internalDrag = false;
  });

  /* 图片加载失败时隐藏，露出文字封面 */
  window.addEventListener('error', (e) => {
    const target = e.target;
    if (target && target.tagName === 'IMG') target.style.display = 'none';
  }, true);

  /* 键盘 */
  function handleKey(e) {
    if (e.metaKey || e.ctrlKey || e.altKey) return false;
    if (e.key === 'Escape' && dialogResolve) {
      settleDialog(false);
      return true;
    }
    if (e.key === 'Escape' && !el.noteScrim.hidden) {
      closeNoteDialog();
      return true;
    }
    if (e.key === 'Escape' && !el.sessionScrim.hidden) {
      el.sessionScrim.hidden = true;
      return true;
    }
    if (e.key === 'Escape' && state.selection) {
      hideAnnoBar();
      clearFrameSelection();
      return true;
    }
    const tag = (e.target && e.target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'select' || tag === 'textarea') return false;

    if (state.view !== 'reader') {
      if (e.key === '/' && state.view === 'library') {
        e.preventDefault();
        el.search.focus();
        return true;
      }
      return false;
    }

    switch (e.key) {
      case 'Escape':
        if (drawerOpen()) closeDrawers();
        else exitReader();
        return true;
      case 'ArrowRight':
        advance(1);
        return true;
      case 'ArrowLeft':
        advance(-1);
        return true;
      case 'r': case 'R':
        toggleTTS();
        return true;
      case 't': case 'T':
        openDrawer('toc', 'toc');
        return true;
      case 'b': case 'B':
        toggleBookmark();
        return true;
      case 's': case 'S':
        openDrawer('settings');
        return true;
      case 'f': case 'F':
        toggleFullscreen();
        return true;
      default:
        return false;
    }
  }

  document.addEventListener('keydown', (e) => {
    if (handleKey(e)) e.preventDefault();
  });

  /* ============================ 路由 & 启动 ============================ */

  function routeFromLocation() {
    if (!state.user) return;
    const hash = location.hash;
    if (hash.startsWith('#/read/')) {
      const id = hash.slice('#/read/'.length);
      if (id) openBook(id);
      return;
    }
    if (hash === '#/admin') {
      openAdmin({ updateHistory: false });
      return;
    }
    enterLibrary({ updateHistory: false });
  }

  window.addEventListener('popstate', () => {
    if (!state.user) return;
    if (state.view === 'reader') {
      saveProgress();
      flushProgress();
      flushReadingTime();
      stopHeartbeat();
    }
    routeFromLocation();
  });

  /* ============================ 登录设备 ============================ */

  function describeUA(ua) {
    const s = String(ua || '');
    let os = '未知系统';
    if (/iPhone/i.test(s)) os = 'iPhone';
    else if (/iPad/i.test(s)) os = 'iPad';
    else if (/Android/i.test(s)) os = 'Android';
    else if (/Windows/i.test(s)) os = 'Windows';
    else if (/Mac OS X|Macintosh/i.test(s)) os = 'macOS';
    else if (/Linux/i.test(s)) os = 'Linux';
    let browser = '未知浏览器';
    if (/Edg\//i.test(s)) browser = 'Edge';
    else if (/OPR\//i.test(s)) browser = 'Opera';
    else if (/Firefox\//i.test(s)) browser = 'Firefox';
    else if (/Chrome\//i.test(s)) browser = 'Chrome';
    else if (/Safari\//i.test(s)) browser = 'Safari';
    return { os, browser, label: `${os} · ${browser}` };
  }

  async function openSessions() {
    el.sessionScrim.hidden = false;
    el.sessionList.innerHTML = '<div class="blank compact">正在加载…</div>';
    await loadSessions();
  }

  async function loadSessions() {
    try {
      const data = await api.sessions();
      const list = data.sessions || [];
      if (list.length === 0) {
        el.sessionList.innerHTML = '<div class="blank compact">没有其它登录设备。</div>';
        return;
      }
      el.sessionList.innerHTML = list.map((sess) => {
        const d = describeUA(sess.userAgent);
        const initial = d.os.slice(0, 1).toUpperCase();
        return `<div class="session-item">
          <div class="session-icon">${esc(initial)}</div>
          <div class="session-main">
            <div class="session-name">${esc(d.label)}${sess.current ? '<span class="session-badge">当前设备</span>' : ''}</div>
            <div class="session-sub">${esc(sess.ip || '未知 IP')} · 最近活跃 ${esc(relativeTime(sess.lastSeen || sess.createdAt))}</div>
          </div>
          <button class="session-revoke" data-revoke="${esc(sess.id)}">${sess.current ? '退出' : '撤销'}</button>
        </div>`;
      }).join('');
    } catch (err) {
      el.sessionList.innerHTML = `<div class="blank compact">${esc(err.message)}</div>`;
    }
  }

  /* ============================ PWA ============================ */

  let deferredInstall = null;

  function isStandalone() {
    return window.matchMedia('(display-mode: standalone)').matches
      || window.navigator.standalone === true;
  }

  function isIOSDevice() {
    return /iphone|ipad|ipod/i.test(navigator.userAgent)
      || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  }

  function setupPWA() {
    if ('serviceWorker' in navigator) {
      window.addEventListener('load', () => {
        navigator.serviceWorker.register('/sw.js', { scope: '/', updateViaCache: 'none' })
          .catch(() => { /* 非安全上下文或浏览器不支持时忽略 */ });
      });
    }
    if (isStandalone() || !el.installEntry) return;
    if (isIOSDevice()) el.installEntry.hidden = false;
    window.addEventListener('beforeinstallprompt', (e) => {
      e.preventDefault();
      deferredInstall = e;
      el.installEntry.hidden = false;
    });
    window.addEventListener('appinstalled', () => {
      deferredInstall = null;
      el.installEntry.hidden = true;
      toast('已安装到桌面');
    });
  }

  async function installApp() {
    if (deferredInstall) {
      const prompt = deferredInstall;
      deferredInstall = null;
      el.installEntry.hidden = true;
      try {
        await prompt.prompt();
        await prompt.userChoice;
      } catch { /* 用户取消 */ }
      return;
    }
    if (isIOSDevice()) {
      toast('在 Safari 底部点「分享」，再选「添加到主屏幕」', { duration: 6000 });
      return;
    }
    toast('可在浏览器菜单中选择「安装应用」或「添加到主屏幕」');
  }

  window.addEventListener('offline', () => toast('网络已断开，恢复连接后可继续保存进度', { type: 'error' }));
  window.addEventListener('online', () => toast('网络已恢复'));

  async function boot() {
    applyAppTheme();
    if (window.I18N) window.I18N.start();
    applyLang();
    setupPWA();
    syncSettingsUI();
    try {
      const s = await api.authState();
      if (s.user) {
        setUser(s.user);
        const match = location.hash.match(/^#\/read\/([\w-]+)/);
        if (match) {
          openBook(match[1]);
        } else if (location.hash === '#/admin') {
          openAdmin({ updateHistory: false });
        } else {
          enterLibrary({ updateHistory: false });
        }
      } else if (s.needsSetup) {
        showAuth('setup');
      } else {
        showAuth('login');
      }
    } catch (err) {
      showAuth('login', '无法连接服务器：' + err.message);
    }
  }

  boot();
})();
