(function () {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const PAGE = 50;
  const H = { 'X-Jobscout': '1', 'Content-Type': 'application/json' };
  // cursors[i] is the cursor that loads page i; page 0 has none.
  const st = { sort: 'published', data: null, seq: 0, cursors: [''], page: 0, saved: [], activeSaved: null };

  const ago = (iso) => {
    if (!iso) return '—';
    const d = Math.floor((Date.now() - Date.parse(iso)) / 864e5);
    return d <= 0 ? 'today' : d === 1 ? '1d ago' : d < 60 ? d + 'd ago' : iso.slice(0, 10);
  };
  const el = (tag, props, ...kids) => {
    const n = document.createElement(tag);
    Object.entries(props || {}).forEach(([k, v]) => { if (k === 'class') n.className = v; else if (k === 'dataset') Object.assign(n.dataset, v); else if (k.startsWith('on')) n.addEventListener(k.slice(2), v); else if (k.includes('-')) n.setAttribute(k, v); else n[k] = v; });
    kids.flat().forEach((k) => k != null && n.append(k));
    return n;
  };
  async function api(path, opts) {
    const r = await fetch(path, opts);
    const body = await r.json().catch(() => ({}));
    if (!r.ok) throw Object.assign(new Error((body.error && body.error.message) || r.statusText), { status: r.status, code: body.error && body.error.code });
    return body;
  }
  const post = (path, body, method) => api(path, { method: method || 'POST', headers: H, body: body ? JSON.stringify(body) : undefined });

  // ---- filters <-> params ----
  function filters() {
    return {
      q: $('q').value.trim(), scope: $('fScope').value, ats: $('fAts').value, workplace: $('fWork').value,
      posted_days: +$('fPosted').value || 0, status: $('fStatus').value, location: $('fLoc').value.trim(), sort: st.sort,
      target_only: $('fCand').checked, new_today: $('fNew').checked, include_closed: $('fClosed').checked, group: $('fGroup').checked,
    };
  }
  function applyFilters(p) {
    $('q').value = p.q || ''; $('fScope').value = p.scope || 'title'; $('fAts').value = p.ats || ''; $('fWork').value = p.workplace || '';
    $('fPosted').value = p.posted_days ? String(p.posted_days) : ''; $('fStatus').value = p.status || ''; $('fLoc').value = p.location || '';
    st.sort = p.sort || 'published'; $('fSort').value = st.sort;
    $('fCand').checked = !!p.target_only; $('fNew').checked = !!p.new_today; $('fClosed').checked = !!p.include_closed; $('fGroup').checked = !!p.group;
  }
  function qs(extra) {
    const f = filters(), p = new URLSearchParams();
    const map = { target_only: 'target' };
    Object.entries(f).forEach(([k, v]) => { if (v !== '' && v !== 0 && v !== false) p.set(map[k] || k, v === true ? '1' : v); });
    Object.entries(extra || {}).forEach(([k, v]) => v && p.set(k, v));
    return p;
  }

  let timer;
  const later = (fn) => { clearTimeout(timer); timer = setTimeout(fn, 160); };

  async function load(reset) {
    if (reset) { st.page = 0; st.cursors = ['']; markTabs(); }
    $('csv').href = '/api/v1/jobs.csv?' + qs();
    const seq = ++st.seq;
    try {
      const data = await api('/api/v1/jobs?' + qs({ limit: PAGE, cursor: st.cursors[st.page] }));
      if (seq !== st.seq) return;
      st.data = data;
      if (data.next_cursor) st.cursors[st.page + 1] = data.next_cursor;
      $('qerr').hidden = true;
      render();
    } catch (e) {
      if (seq !== st.seq) return;
      $('qerr').hidden = false;
      $('qerr').textContent = e.code === 'bad_query' ? 'That search could not be parsed.' : 'Could not reach the server.';
    }
  }

  const COLS = [['title', 'Title'], ['company', 'Company'], ['', 'Location', 'hide-s'], ['published', 'Posted'], ['first_seen', 'First seen', 'hide-s']];
  function render() {
    const d = st.data, startToday = startOfDay();
    $('thead').replaceChildren(...COLS.map(([k, n, c]) =>
      el('th', { class: (c || '') + (st.sort === k ? ' sorted' : ''), dataset: { k } }, n, st.sort === k ? el('span', { class: 'ar' }, ' ↓') : null)));
    const rows = d.jobs.map((j, i) => el('tr', { class: 'job', dataset: { i } },
      el('td', { class: 't' }, j.title,
        j.first_seen >= startToday ? el('span', { class: 'tag new' }, 'NEW') : null,
        j.status ? el('span', { class: 'tag st' }, j.status) : null,
        j.closed_at ? el('span', { class: 'tag st' }, 'closed') : null),
      el('td', {}, j.company_name, ' ', el('span', { class: 'tag ats' }, j.ats)),
      el('td', { class: 'hide-s' }, j.postings > 1
        ? el('span', { title: (j.locations || []).join('\n') }, `${(j.locations || []).length || j.postings} locations`, el('div', { class: 'locs' }, (j.locations || []).slice(0, 3).join(' · ') + ((j.locations || []).length > 3 ? ' …' : '')))
        : (j.location || '—') + (j.workplace ? ' · ' + j.workplace : '')),
      el('td', { class: 'n' }, ago(j.published_at)),
      el('td', { class: 'n hide-s' }, ago(j.first_seen))));
    const firstRun = st.stats && !st.stats.last_scan && !st.stats.total_jobs;
    const empty = firstRun
      ? 'No jobs yet. jobscout is fetching openings from about 150 companies for the first time. This takes about a minute and the list fills in by itself. If nothing happens, click ⟳ Sync now.'
      : 'No openings match. Loosen the search or filters, or click Reset.';
    $('tbody').replaceChildren(...(rows.length ? rows : [el('tr', {}, el('td', { colSpan: 5, class: 'empty' }, empty))]));
    const unit = d.groups !== d.total ? `${d.groups.toLocaleString()} roles (${d.total.toLocaleString()} postings)` : d.total.toLocaleString();
    const from = d.jobs.length ? st.page * PAGE + 1 : 0, to = st.page * PAGE + d.jobs.length;
    $('range').textContent = d.jobs.length ? `${from.toLocaleString()}–${to.toLocaleString()} of ${unit}` : '0 results';
    $('prev').disabled = st.page === 0;
    $('next').disabled = !d.next_cursor;
    $('kpis').replaceChildren(...[[d.total, 'matching openings'], [d.companies, 'companies'], [d.posted_7d, 'posted in last 7 days'], [d.new_today, 'new today']]
      .map(([v, l]) => el('div', { class: 'kpi' }, el('b', {}, Number(v).toLocaleString()), el('span', {}, l))));
    spark(d.per_day || []);
    tops(d.top_companies || []);
  }

  function startOfDay() { const n = new Date(); n.setHours(0, 0, 0, 0); return n.toISOString().slice(0, 19) + 'Z'; }

  function spark(days) {
    const max = Math.max(1, ...days.map((x) => x.count)), W = 270, H2 = 70, bw = W / Math.max(days.length, 1), ns = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(ns, 'svg');
    svg.setAttribute('viewBox', `0 0 ${W} ${H2 + 14}`); svg.setAttribute('width', '100%'); svg.setAttribute('role', 'img'); svg.setAttribute('aria-label', 'Postings per day');
    days.forEach((x, i) => {
      const h = (x.count / max) * H2, r = document.createElementNS(ns, 'rect');
      r.setAttribute('x', i * bw + 1); r.setAttribute('y', H2 - h); r.setAttribute('width', Math.max(bw - 2, 1)); r.setAttribute('height', x.count ? Math.max(h, 1) : 0);
      r.setAttribute('fill', i === days.length - 1 ? 'var(--bar)' : 'var(--bar-dim)');
      const t = document.createElementNS(ns, 'title'); t.textContent = `${x.name}: ${x.count} posted`; r.appendChild(t); svg.appendChild(r);
    });
    const lab = (x, anchor, txt) => { const t = document.createElementNS(ns, 'text'); t.setAttribute('x', x); t.setAttribute('y', H2 + 11); t.setAttribute('text-anchor', anchor); t.textContent = txt; svg.appendChild(t); };
    lab(0, 'start', '30d ago'); lab(W, 'end', `today · max ${max}/day`);
    $('spark').replaceChildren(svg);
  }

  function tops(list) {
    if (!list.length) { $('tops').textContent = '—'; return; }
    const mx = list[0].count;
    $('tops').replaceChildren(...list.map((c) => {
      const bar = el('i'); bar.style.width = Math.max(2, (c.count / mx) * 100) + '%';
      return el('div', { class: 'cbar', dataset: { c: c.name } }, el('span', { class: 'nm' }, c.label || c.name), el('span', { class: 'ct' }, c.count), el('div', { class: 'bar' }, bar));
    }));
  }

  // ---- saved searches ----
  async function loadSaved() {
    try { st.saved = (await api('/api/v1/saved-searches')).saved_searches || []; } catch (e) { st.saved = []; }
    const all = el('button', { onclick: () => { st.activeSaved = null; applyFilters({ target_only: false }); load(true); } }, 'All jobs');
    $('tabs').replaceChildren(all, ...st.saved.map((s) => el('button', { dataset: { id: s.id }, title: s.params.q || '', onclick: () => openSaved(s) },
      s.name, s.new_count ? el('span', { class: 'badge', title: 'new since you last looked' }, s.new_count) : null)));
    markTabs();
  }
  function markTabs() {
    [...$('tabs').children].forEach((b) => b.classList.toggle('on', (b.dataset.id ? +b.dataset.id : null) === st.activeSaved));
  }
  async function openSaved(s) {
    st.activeSaved = s.id; applyFilters(s.params); await load(true);
    try { await post(`/api/v1/saved-searches/${s.id}/viewed`); } catch (e) { /* non-fatal */ }
    loadSaved();
  }
  $('saveSearch').addEventListener('click', async () => {
    const cur = st.saved.find((s) => s.id === st.activeSaved);
    const name = prompt('Name this search', cur ? cur.name : ($('q').value.trim().slice(0, 40) || 'My search'));
    if (!name) return;
    const f = filters();
    try { const r = await post('/api/v1/saved-searches', { name, params: f }); st.activeSaved = r.id; loadSaved(); } catch (e) { alert(e.message); }
  });

  // ---- detail row ----
  const STATUSES = ['', 'saved', 'applied', 'interviewing', 'offer', 'rejected'];
  async function openDetail(tr) {
    const next = tr.nextElementSibling;
    if (next && next.classList.contains('detail')) { next.remove(); return; }
    document.querySelectorAll('tr.detail').forEach((x) => x.remove());
    const td = el('td', { colSpan: 5 }, 'Loading…');
    tr.after(el('tr', { class: 'detail' }, td));
    try { td.replaceChildren(detailNode(await api('/api/v1/jobs/' + st.data.jobs[+tr.dataset.i].id), st.data.jobs[+tr.dataset.i])); } catch (e) { td.textContent = 'Could not load this job.'; }
  }

  function detailNode(j, row) {
    const kv = [['Company', j.company_name], ['Source', j.ats], ['Location', row.postings > 1 ? (row.locations || []).join(' · ') : j.location], ['Workplace', j.workplace],
      ['Type', j.employment_type], ['Department', [j.department, j.team].filter(Boolean).join(' / ')], ['Compensation', j.compensation],
      ['Posted', (j.published_at || '').slice(0, 10)], ['First seen', (j.first_seen || '').slice(0, 10)], ['Last seen', (j.last_seen || '').slice(0, 10)], ['Closed', (j.closed_at || '').slice(0, 10)]]
      .filter((x) => x[1]).map(([k, v]) => el('div', {}, el('span', {}, k), v));
    const sel = el('select', { 'aria-label': 'Status' }, ...STATUSES.map((s) => el('option', { value: s, selected: s === j.status }, s || '(no status)')));
    sel.addEventListener('change', async () => { try { await post(`/api/v1/jobs/${j.id}/status`, { status: sel.value }); load(false); } catch (e) { alert('Could not save status: ' + e.message); } });
    const note = el('input', { type: 'text', placeholder: 'Add a note…', 'aria-label': 'Note' });
    const notes = el('ul', { class: 'notes' });
    const renderNotes = (list) => notes.replaceChildren(...(list || []).map((n) => el('li', {}, `${(n.ts || '').slice(0, 10)} · ${n.actor}: ${n.note}`)));
    renderNotes(j.notes);
    const add = el('button', { onclick: async () => {
      if (!note.value.trim()) return;
      try { await post(`/api/v1/jobs/${j.id}/notes`, { note: note.value }); note.value = ''; renderNotes((await api('/api/v1/jobs/' + j.id)).notes); } catch (e) { alert('Could not save note: ' + e.message); }
    } }, 'Add note');
    const desc = el('div', { class: 'desc' }, j.description || '(no description stored)');
    highlight(desc);
    return el('div', {}, el('a', { class: 'apply', href: j.url, target: '_blank', rel: 'noopener noreferrer' }, 'Open posting ↗'),
      el('div', { class: 'dgrid' }, kv), el('div', { class: 'actions' }, sel, note, add), notes, desc);
  }

  function highlight(node) {
    const terms = ($('q').value.match(/"[^"]+"|[^\s()"]+/g) || []).filter((t) => !/^(OR|AND|NOT|\||\|\||&&|-.*)$/.test(t) && !/^\w+:/.test(t))
      .map((t) => t.replace(/^"|"$/g, '').replace(/\*$/, '')).filter((t) => t.length > 1);
    if (!terms.length) return;
    const re = new RegExp('(?<![A-Za-z0-9])(' + terms.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|') + ')', 'gi');
    const text = node.textContent; node.replaceChildren(); let last = 0, m;
    while ((m = re.exec(text))) { node.append(text.slice(last, m.index), el('mark', {}, m[0])); last = m.index + m[0].length; if (m[0] === '') re.lastIndex++; }
    node.append(text.slice(last));
  }

  // ---- boards dialog ----
  async function loadBoards() {
    const body = $('boardsBody');
    try {
      const { boards } = await api('/api/v1/boards');
      body.replaceChildren(...boards.map((b) => {
        const status = b.disabled ? el('span', { class: 'st-bad' }, 'disabled') : b.last_status === 'ok' ? el('span', { class: 'st-ok' }, 'ok') : el('span', { class: b.last_status ? 'st-bad' : '' }, b.last_status || 'not scanned');
        const toggle = el('button', { onclick: async () => { try { await post(`/api/v1/boards/${b.ats}/${encodeURIComponent(b.slug)}`, { enabled: b.disabled }, 'PATCH'); loadBoards(); } catch (e) { alert(e.message); } } }, b.disabled ? 'Enable' : 'Disable');
        const rename = el('button', { onclick: async () => {
          const n = prompt('Company name', b.display_name); if (n == null) return;
          try { await post(`/api/v1/boards/${b.ats}/${encodeURIComponent(b.slug)}`, { display_name: n }, 'PATCH'); loadBoards(); load(false); } catch (e) { alert(e.message); }
        } }, 'Rename');
        return el('tr', {}, el('td', { title: b.slug }, b.display_name), el('td', {}, b.ats), el('td', { title: b.last_error || '' }, status), el('td', { class: 'n' }, b.job_count), el('td', {}, rename, ' ', toggle));
      }));
    } catch (e) { body.replaceChildren(el('tr', {}, el('td', { colSpan: 5 }, 'Could not load boards.'))); }
  }
  $('boardsBtn').addEventListener('click', () => { $('boardMsg').textContent = ''; $('boardsDlg').showModal(); loadBoards(); });
  $('boardsClose').addEventListener('click', () => $('boardsDlg').close());
  $('addBoard').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    try {
      const r = await post('/api/v1/boards', { url: $('boardUrl').value });
      $('boardMsg').textContent = r.added ? `Added ${r.ats}/${r.slug}. Click Sync now to fetch its jobs.` : `${r.ats}/${r.slug} is already tracked.`;
      $('boardUrl').value = ''; loadBoards();
    } catch (e) { $('boardMsg').textContent = e.message; }
  });

  // ---- events ----
  $('tbody').addEventListener('click', (e) => { const tr = e.target.closest('tr.job'); if (tr) openDetail(tr); });
  $('thead').addEventListener('click', (e) => { const th = e.target.closest('th'); if (!th || !th.dataset.k) return; st.sort = th.dataset.k; $('fSort').value = st.sort; load(true); });
  $('fSort').addEventListener('change', () => { st.sort = $('fSort').value; load(true); });
  $('tops').addEventListener('click', (e) => { const c = e.target.closest('.cbar'); if (!c) return; $('q').value = ($('q').value + ' company:' + c.dataset.c).trim(); load(true); });
  $('q').addEventListener('input', () => later(() => load(true)));
  $('fLoc').addEventListener('input', () => later(() => load(true)));
  ['fScope', 'fAts', 'fWork', 'fPosted', 'fStatus', 'fCand', 'fNew', 'fClosed', 'fGroup'].forEach((id) => $(id).addEventListener('change', () => load(true)));
  $('prev').addEventListener('click', () => { if (st.page > 0) { st.page--; load(false); window.scrollTo({ top: 0 }); } });
  $('next').addEventListener('click', () => { if (st.cursors[st.page + 1]) { st.page++; load(false); window.scrollTo({ top: 0 }); } });
  $('reset').addEventListener('click', () => { st.activeSaved = null; applyFilters({ target_only: true }); load(true); });

  // ---- sync ----
  const syncBtn = $('sync'), syncMsg = $('syncmsg'); let poll;
  const setSync = (m, err) => { syncMsg.textContent = m || ''; syncMsg.className = 'meta' + (err ? ' err' : ''); };
  async function checkScan() {
    try {
      const s = await api('/api/v1/scan');
      if (s.state === 'running') { syncBtn.disabled = true; syncBtn.textContent = 'Syncing…'; setSync(s.message); return 'running'; }
      syncBtn.disabled = false; syncBtn.textContent = '⟳ Sync now';
      if (s.state === 'error') setSync(s.message, true); else if (s.state === 'done') setSync(s.message);
      return s.state;
    } catch (e) { setSync('Server not reachable.', true); syncBtn.disabled = false; return 'error'; }
  }
  function startPolling() {
    clearInterval(poll);
    poll = setInterval(async () => {
      const s = await checkScan();
      if (s !== 'running') { clearInterval(poll); if (s === 'done') { loadMeta(); loadOptions(); loadSaved(); load(false); } }
    }, 1200);
  }
  syncBtn.addEventListener('click', async () => {
    syncBtn.disabled = true; setSync('Starting…');
    try { await post('/api/v1/scan'); } catch (e) { if (e.status !== 409) { setSync('Could not start sync: ' + e.message, true); syncBtn.disabled = false; return; } }
    await checkScan(); startPolling();
  });

  async function loadMeta() {
    try {
      const s = await api('/api/v1/stats');
      st.stats = s;
      $('meta').textContent = `${s.open_jobs.toLocaleString()} open · ${s.boards} boards${s.disabled_boards ? ` (${s.disabled_boards} disabled)` : ''} · last scan ${s.last_scan ? new Date(s.last_scan).toLocaleString() : 'never'}`;
    } catch (e) { /* shown by other calls */ }
  }
  async function loadOptions() {
    try {
      const o = await api('/api/v1/options');
      const fill = (sel, list) => { const keep = sel.value; [...sel.options].slice(1).forEach((x) => x.remove()); list.forEach((f) => sel.appendChild(el('option', { value: f.name }, `${f.name} (${f.count.toLocaleString()})`))); sel.value = keep; };
      fill($('fAts'), o.ats || []); fill($('fWork'), o.workplace || []);
    } catch (e) { /* ignore */ }
  }

  loadMeta().then(() => load(true)); loadOptions(); loadSaved();
  checkScan().then((s) => { if (s === 'running') startPolling(); });
})();
