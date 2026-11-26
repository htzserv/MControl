/* MControl v1.4.0 — Commercial Multi-Tenant SaaS Control Plane + Persistent Drag & Drop */
(function () {
  'use strict';

  var SAAS_ME = null;
  var SAAS_ORDER = { servers: [], tunnels: [] };
  var dndDraggingEl = null;
  var dndDraggingKind = null;

  function saasEsc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  function injectStyles() {
    if (document.getElementById('mc-saas-css')) return;
    var st = document.createElement('style');
    st.id = 'mc-saas-css';
    st.textContent = [
      '#mc-imp-banner{position:sticky;top:0;z-index:950;background:linear-gradient(90deg,rgba(250,204,21,.22),rgba(245,158,11,.28));border-bottom:1.5px solid var(--yellow,#f59e0b);padding:9px 18px;display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap;backdrop-filter:blur(14px);}',
      '.mc-dnd-handle{cursor:grab;opacity:.55;padding:2px 6px;border-radius:8px;font-size:.95rem;user-select:none;transition:opacity .15s,color .15s;display:inline-flex;align-items:center;}',
      '.mc-dnd-handle:hover{opacity:1;color:var(--accent,#6366f1);}',
      '.mc-dnd-dragging{opacity:.42 !important;transform:scale(.99);}',
      '.mc-dnd-over{outline:2px dashed var(--accent,#6366f1) !important;outline-offset:2px;}',
      '.saas-row{display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap;padding:11px 14px;border-radius:min(var(--rc,14px),18px);background:var(--row,rgba(255,255,255,.04));border:var(--bw,1px) solid var(--border,rgba(148,163,184,.18));}',
      '.saas-stat-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:10px;margin-bottom:14px;}',
      '.saas-stat-box{padding:11px 14px;border-radius:min(var(--rc,14px),18px);background:var(--row,rgba(255,255,255,.04));border:var(--bw,1px) solid var(--border,rgba(148,163,184,.18));}',
      '@media (max-width:768px){#saas-modal .modal{width:96vw !important;max-width:96vw !important;max-height:92vh !important;padding:12px !important;}#saas-modal .row2{grid-template-columns:1fr !important;}.saas-stat-grid{grid-template-columns:repeat(2,minmax(0,1fr)) !important;}.saas-row{flex-direction:column;align-items:flex-start !important;}#mc-imp-banner{padding:7px 12px;font-size:.78rem;}}'
    ].join('\n');
    document.head.appendChild(st);
  }

  function ensureUIElements() {
    injectStyles();

    // 1. SuperAdmin Pill Button in #chrome next to Backup bubble
    var bakBtn = document.getElementById('nav-bak-bubble');
    if (bakBtn && !document.getElementById('nav-saas-bubble')) {
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.id = 'nav-saas-bubble';
      btn.className = 'ver-bubble';
      btn.style.display = 'none';
      btn.title = 'Commercial Tenants (SuperAdmin)';
      btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round" stroke-linejoin="round" style="width:15px;height:15px;color:var(--accent);"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg><span class="bak-bubble-lbl">Tenants</span>';
      btn.addEventListener('click', openSaasModal);
      bakBtn.parentNode.insertBefore(btn, bakBtn.nextSibling);
    }

    // 2. SuperAdmin Modal
    if (!document.getElementById('saas-modal')) {
      var m = document.createElement('div');
      m.id = 'saas-modal';
      m.className = 'modal-bg';
      m.style.display = 'none';
      m.innerHTML = [
        '<div class="modal" style="max-width:860px;width:95vw;max-height:90vh;overflow:auto;">',
        '  <div class="toolbar" style="margin-bottom:12px;">',
        '    <h3 style="color:var(--accent);margin:0;">SuperAdmin — Commercial Multi-Tenant Control</h3>',
        '    <button type="button" class="btn btn-danger btn-sm" id="saas-close-btn">Close</button>',
        '  </div>',
        '  <div id="saas-stats" class="saas-stat-grid"></div>',
        '  <form id="saas-create-form" class="card" style="margin-bottom:14px;">',
        '    <h4 style="margin-bottom:10px;color:var(--accent);">Create Isolated Tenant (Dedicated SQLite DB)</h4>',
        '    <div class="row2">',
        '      <div class="field"><label>Username</label><input id="saas-u" placeholder="customer01" required></div>',
        '      <div class="field"><label>Password (min 6 chars)</label><input id="saas-p" type="password" placeholder="••••••" required></div>',
        '    </div>',
        '    <div class="row2">',
        '      <div class="field"><label>Max Servers</label><input id="saas-ms" type="number" value="10" min="1"></div>',
        '      <div class="field"><label>Max Tunnels</label><input id="saas-mt" type="number" value="20" min="1"></div>',
        '    </div>',
        '    <div class="row2">',
        '      <div class="field"><label>Max Monitors</label><input id="saas-mm" type="number" value="20" min="1"></div>',
        '      <div class="field"><label>Subscription Days (0 = Unlimited)</label><input id="saas-exp" type="number" value="30" min="0"></div>',
        '    </div>',
        '    <button type="submit" class="btn btn-sky btn-sm">+ Create Isolated Tenant</button>',
        '  </form>',
        '  <div class="card">',
        '    <div class="toolbar" style="margin-bottom:10px;"><strong>Managed Tenants</strong><button type="button" class="btn btn-ghost btn-sm" id="saas-refresh-btn">Refresh</button></div>',
        '    <div id="saas-list" style="display:flex;flex-direction:column;gap:8px;">Loading...</div>',
        '  </div>',
        '</div>'
      ].join('');
      document.body.appendChild(m);
      m.addEventListener('mousedown', function (e) { if (e.target === m) m.style.display = 'none'; });
      document.getElementById('saas-close-btn').addEventListener('click', function () { m.style.display = 'none'; });
      document.getElementById('saas-refresh-btn').addEventListener('click', loadSaasTenants);
      document.getElementById('saas-create-form').addEventListener('submit', createSaasTenant);
    }
  }

  function applyRoleChrome() {
    ensureUIElements();
    var saasBtn = document.getElementById('nav-saas-bubble');
    var verBtn = document.getElementById('nav-ver-bubble');
    var bakBtn = document.getElementById('nav-bak-bubble');
    var oldBanner = document.getElementById('mc-imp-banner');
    if (oldBanner) oldBanner.remove();

    if (!SAAS_ME) return;
    var isSuper = SAAS_ME.role === 'superadmin' && !SAAS_ME.impersonating;
    if (saasBtn) saasBtn.style.display = isSuper ? 'inline-flex' : 'none';
    var defThBtn = document.getElementById('btn-set-default-theme');
    if (defThBtn) defThBtn.style.display = isSuper ? 'inline-flex' : 'none';

    // Hide host-level system controls for tenants (keep everything else 100% intact)
    if (!isSuper) {
      if (verBtn) verBtn.style.display = 'none';
      if (bakBtn) bakBtn.style.display = 'none';
      var outTab = document.getElementById('stab-outbound');
      if (outTab) outTab.style.display = 'none';
    } else {
      if (verBtn) verBtn.style.display = '';
      if (bakBtn) bakBtn.style.display = '';
      var outTab2 = document.getElementById('stab-outbound');
      if (outTab2) outTab2.style.display = '';
    }

    // Impersonation banner for SuperAdmin
    if (SAAS_ME.impersonating) {
      var bar = document.createElement('div');
      bar.id = 'mc-imp-banner';
      bar.innerHTML = '<div><strong>Impersonating Tenant: ' + saasEsc(SAAS_ME.username) + '</strong> <span class="mono" style="font-size:.78rem;opacity:.85;">(' + saasEsc(SAAS_ME.db_file) + ' • Quota: ' + SAAS_ME.max_servers + ' srv / ' + SAAS_ME.max_tunnels + ' tun)</span></div><button type="button" class="btn btn-warn btn-sm" id="mc-exit-imp">Return to SuperAdmin</button>';
      document.body.insertBefore(bar, document.body.firstChild);
      document.getElementById('mc-exit-imp').addEventListener('click', exitImpersonation);
    }
  }

  async function refreshSaasIdentity() {
    if (typeof api !== 'function') return;
    var r = await api('api/saas/me');
    if (r && r.ok) {
      SAAS_ME = r;
      applyRoleChrome();
      if (typeof window.loadServerThemeProfile === 'function') window.loadServerThemeProfile();
      if (typeof window.loadBrandProfile === 'function') window.loadBrandProfile();
      var ord = await api('api/saas/order');
      if (ord && ord.ok) {
        SAAS_ORDER.servers = Array.isArray(ord.servers) ? ord.servers : [];
        SAAS_ORDER.tunnels = Array.isArray(ord.tunnels) ? ord.tunnels : [];
        reorderDOM();
      }
    }
  }

  function openSaasModal() {
    ensureUIElements();
    document.getElementById('saas-modal').style.display = 'flex';
    loadSaasTenants();
  }

  async function loadSaasTenants() {
    var box = document.getElementById('saas-list');
    var stBox = document.getElementById('saas-stats');
    var r = await api('api/saas/tenants');
    if (!r || !r.ok) {
      box.innerHTML = '<div style="color:var(--red);">' + saasEsc((r && r.error) || 'Failed to load tenants') + '</div>';
      return;
    }
    var list = r.tenants || [];
    stBox.innerHTML = [
      '<div class="saas-stat-box"><div style="font-size:.73rem;color:var(--muted);">Total Tenants</div><div style="font-size:1.35rem;font-weight:800;color:var(--accent);">' + list.length + '</div></div>',
      '<div class="saas-stat-box"><div style="font-size:.73rem;color:var(--muted);">Active Tenants</div><div style="font-size:1.35rem;font-weight:800;color:var(--green);">' + (r.active_tenants || 0) + '</div></div>',
      '<div class="saas-stat-box"><div style="font-size:.73rem;color:var(--muted);">Fleet Servers (All DBs)</div><div style="font-size:1.35rem;font-weight:800;">' + (r.total_servers || 0) + '</div></div>',
      '<div class="saas-stat-box"><div style="font-size:.73rem;color:var(--muted);">Fleet Tunnels (All DBs)</div><div style="font-size:1.35rem;font-weight:800;">' + (r.total_tunnels || 0) + '</div></div>'
    ].join('');

    if (!list.length) {
      box.innerHTML = '<div style="color:var(--muted);font-size:.82rem;">No tenants created yet. Use the form above to spin up an isolated customer workspace.</div>';
      return;
    }
    box.innerHTML = list.map(function (t) {
      var exp = t.expires_at ? new Date(t.expires_at * 1000).toLocaleDateString() : 'Unlimited';
      var statusUrl = location.origin + '/status/' + encodeURIComponent(t.username);
      return '<div class="saas-row">' +
        '<div>' +
          '<div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">' +
            '<strong>' + saasEsc(t.username) + '</strong>' +
            '<span class="badge ' + (t.active ? 'b-ok' : 'b-miss') + '">' + (t.active ? 'ACTIVE' : 'SUSPENDED') + '</span>' +
            '<span class="mono" style="font-size:.74rem;color:var(--muted);">u_' + t.id + '/mcontrol.db</span>' +
          '</div>' +
          '<div class="mono" style="font-size:.74rem;color:var(--muted);margin-top:4px;">' +
            'Servers: ' + t.server_count + '/' + t.max_servers + ' • Tunnels: ' + t.tunnel_count + '/' + t.max_tunnels + ' • Monitors: ' + t.monitor_count + '/' + t.max_monitors + ' • Exp: ' + saasEsc(exp) +
            ' • <a href="' + saasEsc(statusUrl) + '" target="_blank" style="color:var(--accent);">/status/' + saasEsc(t.username) + '</a>' +
          '</div>' +
        '</div>' +
        '<div style="display:flex;gap:6px;flex-wrap:wrap;">' +
          '<button type="button" class="btn btn-sky btn-sm" data-saas-imp="' + t.id + '">Impersonate</button>' +
          '<button type="button" class="btn btn-warn btn-sm" data-saas-tog="' + t.id + '" data-saas-act="' + (t.active ? '0' : '1') + '">' + (t.active ? 'Suspend' : 'Activate') + '</button>' +
          '<button type="button" class="btn btn-danger btn-sm" data-saas-del="' + t.id + '" data-saas-name="' + saasEsc(t.username) + '">Delete</button>' +
        '</div>' +
      '</div>';
    }).join('');

    box.querySelectorAll('[data-saas-imp]').forEach(function (b) {
      b.addEventListener('click', function () { impersonateTenant(Number(b.dataset.saasImp)); });
    });
    box.querySelectorAll('[data-saas-tog]').forEach(function (b) {
      b.addEventListener('click', function () { toggleTenant(Number(b.dataset.saasTog), b.dataset.saasAct === '1'); });
    });
    box.querySelectorAll('[data-saas-del]').forEach(function (b) {
      b.addEventListener('click', function () { deleteTenant(Number(b.dataset.saasDel), b.dataset.saasName); });
    });
  }

  async function createSaasTenant(e) {
    e.preventDefault();
    var r = await api('api/saas/tenants', 'POST', {
      username: document.getElementById('saas-u').value.trim(),
      password: document.getElementById('saas-p').value,
      max_servers: Number(document.getElementById('saas-ms').value) || 10,
      max_tunnels: Number(document.getElementById('saas-mt').value) || 20,
      max_monitors: Number(document.getElementById('saas-mm').value) || 20,
      expires_days: Number(document.getElementById('saas-exp').value) || 0
    });
    if (r && r.ok) {
      if (typeof toast === 'function') toast('Tenant created with isolated SQLite DB!');
      document.getElementById('saas-u').value = '';
      document.getElementById('saas-p').value = '';
      loadSaasTenants();
    } else if (typeof toast === 'function') {
      toast((r && r.error) || 'Failed to create tenant', true);
    }
  }

  async function toggleTenant(id, active) {
    var r = await api('api/saas/tenants/' + id, 'PUT', { active: active });
    if (r && r.ok) {
      if (typeof toast === 'function') toast(active ? 'Tenant reactivated.' : 'Tenant suspended.');
      loadSaasTenants();
    }
  }

  async function deleteTenant(id, name) {
    if (!confirm('Delete tenant "' + name + '" and permanently wipe their isolated SQLite database?')) return;
    var r = await api('api/saas/tenants/' + id, 'DELETE');
    if (r && r.ok) {
      if (typeof toast === 'function') toast('Tenant & isolated DB removed.');
      loadSaasTenants();
    }
  }

  async function impersonateTenant(id) {
    var r = await api('api/saas/tenants/' + id + '/impersonate', 'POST');
    if (r && r.ok && r.token) {
      localStorage.setItem('mc_token', r.token);
      location.reload();
    } else if (typeof toast === 'function') {
      toast((r && r.error) || 'Impersonation failed', true);
    }
  }

  async function exitImpersonation() {
    var r = await api('api/saas/exit-impersonate', 'POST');
    if (r && r.ok && r.token) {
      localStorage.setItem('mc_token', r.token);
      location.reload();
    }
  }

  // ── Persistent Drag & Drop for Server & Tunnel Cards ───────────────────────

  function applyOrderToContainer(container, attr, orderArr) {
    if (!container || !orderArr || !orderArr.length) return;
    var rank = {};
    orderArr.forEach(function (id, idx) { rank[String(id)] = idx; });
    var kids = Array.prototype.slice.call(container.children).filter(function (el) { return el.hasAttribute(attr); });
    if (kids.length < 2) return;
    var sorted = kids.slice().sort(function (a, b) {
      var ra = rank.hasOwnProperty(a.getAttribute(attr)) ? rank[a.getAttribute(attr)] : 999999;
      var rb = rank.hasOwnProperty(b.getAttribute(attr)) ? rank[b.getAttribute(attr)] : 999999;
      return ra - rb;
    });
    var changed = false;
    for (var i = 0; i < sorted.length; i++) {
      if (sorted[i] !== kids[i]) { changed = true; break; }
    }
    if (changed) {
      sorted.forEach(function (el) { container.appendChild(el); });
    }
  }

  function wireDndOnContainer(container, attr, kind) {
    if (!container) return;
    var cards = container.querySelectorAll('[' + attr + ']');
    cards.forEach(function (card) {
      if (card.dataset.dndWired === '1') return;
      card.dataset.dndWired = '1';
      card.setAttribute('draggable', 'true');

      var head = card.querySelector('.srv-row-head > div > div');
      if (head && !head.querySelector('.mc-dnd-handle')) {
        var grip = document.createElement('span');
        grip.className = 'mc-dnd-handle';
        grip.title = 'Drag to reorder';
        grip.textContent = '⋮⋮';
        grip.addEventListener('click', function (ev) { ev.stopPropagation(); });
        head.insertBefore(grip, head.firstChild);
      }

      card.addEventListener('dragstart', function (ev) {
        dndDraggingEl = card;
        dndDraggingKind = kind;
        card.classList.add('mc-dnd-dragging');
        if (ev.dataTransfer) {
          ev.dataTransfer.effectAllowed = 'move';
          ev.dataTransfer.setData('text/plain', card.getAttribute(attr) || '');
        }
      });

      card.addEventListener('dragend', function () {
        card.classList.remove('mc-dnd-dragging');
        container.querySelectorAll('.mc-dnd-over').forEach(function (x) { x.classList.remove('mc-dnd-over'); });
        dndDraggingEl = null;
        dndDraggingKind = null;
      });

      card.addEventListener('dragover', function (ev) {
        if (!dndDraggingEl || dndDraggingEl === card || dndDraggingKind !== kind) return;
        ev.preventDefault();
        card.classList.add('mc-dnd-over');
      });

      card.addEventListener('dragleave', function () {
        card.classList.remove('mc-dnd-over');
      });

      card.addEventListener('drop', function (ev) {
        if (!dndDraggingEl || dndDraggingEl === card || dndDraggingKind !== kind) return;
        ev.preventDefault();
        card.classList.remove('mc-dnd-over');
        var rect = card.getBoundingClientRect();
        var after = (ev.clientY - rect.top) > (rect.height / 2);
        if (after) container.insertBefore(dndDraggingEl, card.nextSibling);
        else container.insertBefore(dndDraggingEl, card);

        var ids = Array.prototype.slice.call(container.querySelectorAll('[' + attr + ']')).map(function (el) {
          return Number(el.getAttribute(attr));
        }).filter(Boolean);
        SAAS_ORDER[kind] = ids;
        if (kind === 'servers') {
          var other = container.id === 'servers-grid' ? document.getElementById('overview-grid') : document.getElementById('servers-grid');
          applyOrderToContainer(other, attr, ids);
        }
        api('api/saas/order', 'PUT', { kind: kind, ids: ids }).then(function (r) {
          if (r && r.ok && typeof toast === 'function') toast('Card order saved!');
        });
      });
    });
  }

  function reorderDOM() {
    var sg = document.getElementById('servers-grid');
    var og = document.getElementById('overview-grid');
    var tl = document.getElementById('tunnels-list');
    applyOrderToContainer(sg, 'data-srv-id', SAAS_ORDER.servers);
    applyOrderToContainer(og, 'data-srv-id', SAAS_ORDER.servers);
    applyOrderToContainer(tl, 'data-tun-id', SAAS_ORDER.tunnels);
    wireDndOnContainer(sg, 'data-srv-id', 'servers');
    wireDndOnContainer(og, 'data-srv-id', 'servers');
    wireDndOnContainer(tl, 'data-tun-id', 'tunnels');
  }

  // Hook into MControl's enterApp & DOM updates cleanly
  function bootstrap() {
    ensureUIElements();
    if (typeof window.enterApp === 'function' && !window._mcSaasWrappedEnter) {
      window._mcSaasWrappedEnter = true;
      var origEnter = window.enterApp;
      window.enterApp = function () {
        var res = origEnter.apply(this, arguments);
        refreshSaasIdentity();
        return res;
      };
    }
    var obs = new MutationObserver(function () { reorderDOM(); });
    ['servers-grid', 'overview-grid', 'tunnels-list'].forEach(function (id) {
      var el = document.getElementById(id);
      if (el) obs.observe(el, { childList: true });
    });
    if (localStorage.getItem('mc_token')) {
      setTimeout(refreshSaasIdentity, 400);
    }
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', bootstrap);
  else bootstrap();
})();
