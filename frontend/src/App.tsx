import React, { useEffect, useState } from 'react';
import {
  LayoutGrid,
  Server as ServerIcon,
  Network,
  Shield,
  Palette,
  LogOut,
  Sun,
  Moon,
  AlertTriangle,
  Activity,
  Globe,
  Cloud,
} from 'lucide-react';
import { api, getToken, setToken } from './api';
import { ServersView } from './components/ServersView';
import { TopologyView } from './components/TopologyView';
import { AdminView } from './components/AdminView';
import { UptimeView } from './components/UptimeView';
import { DNSView } from './components/DNSView';
import { CloudView } from './components/CloudView';
import type { Branding, ServerItem, ThemePrefs, TunnelItem, User } from './types';

type Tab = 'overview' | 'servers' | 'topology' | 'uptime' | 'dns' | 'cloud' | 'admin' | 'theme';

export function App() {
  const [loading, setLoading] = useState(true);
  const [initialized, setInitialized] = useState(true);
  const [version, setVersion] = useState('1.0.0');
  const [branding, setBranding] = useState<Branding>({
    app_name: 'MControl',
    subtitle: 'Multi-Tenant Infrastructure Cloud',
    accent: '#6366f1',
  });
  const [user, setUser] = useState<User | null>(null);
  const [impersonatedBy, setImpersonatedBy] = useState(0);
  const [tab, setTab] = useState<Tab>('overview');

  const [servers, setServers] = useState<ServerItem[]>([]);
  const [tunnels, setTunnels] = useState<TunnelItem[]>([]);
  const [theme, setTheme] = useState<ThemePrefs>({
    preset: 'mdesign',
    mode: 'dark',
    accent: '#6366f1',
    radius: 20,
    lang: 'fa',
  });

  // Auth / Setup fields
  const [uInp, setUInp] = useState('');
  const [pInp, setPInp] = useState('');
  const [brandInp, setBrandInp] = useState('MControl');
  const [toast, setToast] = useState<{ msg: string; err: boolean } | null>(null);

  function notify(msg: string, err = false) {
    setToast({ msg, err });
    window.setTimeout(() => setToast(null), 3200);
  }

  useEffect(() => {
    document.body.dataset.mode = theme.mode;
    document.body.dataset.preset = theme.preset;
    document.documentElement.dir = theme.lang === 'fa' ? 'rtl' : 'ltr';
    document.documentElement.lang = theme.lang;
    document.body.style.setProperty('--accent', theme.accent || branding.accent || '#6366f1');
    document.body.style.setProperty('--radius', `${theme.radius}px`);
  }, [theme, branding]);

  async function loadWorkspace() {
    const [rs, rt] = await Promise.all([
      api<{ servers: ServerItem[] }>('/api/tenant/servers'),
      api<{ tunnels: TunnelItem[] }>('/api/tenant/tunnels'),
    ]);
    if (rs.ok && rs.servers) setServers(rs.servers);
    if (rt.ok && rt.tunnels) setTunnels(rt.tunnels);
  }

  async function boot() {
    setLoading(true);
    const st = await api<{ initialized: boolean; version: string; branding: Branding }>('/api/setup/status');
    if (st.ok) {
      setInitialized(!!st.initialized);
      if (st.version) setVersion(st.version);
      if (st.branding) setBranding(st.branding);
    }
    if (st.initialized && getToken()) {
      const me = await api<{
        user: User;
        impersonated_by: number;
        branding: Branding;
        theme: ThemePrefs;
      }>('/api/auth/me');
      if (me.ok && me.user) {
        setUser(me.user);
        setImpersonatedBy(me.impersonated_by || 0);
        if (me.branding) setBranding(me.branding);
        if (me.theme) setTheme(me.theme);
        await loadWorkspace();
      } else {
        setToken('');
        setUser(null);
      }
    }
    setLoading(false);
  }

  useEffect(() => {
    boot();
  }, []);

  async function handleInitSetup(e: React.FormEvent) {
    e.preventDefault();
    const r = await api<{ token: string; legacy_v1_migrated?: boolean }>('/api/setup/init', 'POST', {
      username: uInp,
      password: pInp,
      app_name: brandInp,
    });
    if (r.ok && r.token) {
      setToken(r.token);
      notify(r.legacy_v1_migrated ? 'Initialized & v1 DB backed up!' : 'SuperAdmin initialized!');
      await boot();
    } else notify(r.error || 'Setup failed', true);
  }

  async function handleLogin(e: React.FormEvent) {
    e.preventDefault();
    const r = await api<{ token: string }>('/api/auth/login', 'POST', { username: uInp, password: pInp });
    if (r.ok && r.token) {
      setToken(r.token);
      setPInp('');
      await boot();
    } else notify(r.error || 'Invalid credentials', true);
  }

  async function handleExitImpersonate() {
    const r = await api<{ token: string }>('/api/auth/exit-impersonate', 'POST');
    if (r.ok && r.token) {
      setToken(r.token);
      setTab('admin');
      await boot();
      notify('Returned to SuperAdmin session.');
    }
  }

  function logout() {
    setToken('');
    setUser(null);
    setImpersonatedBy(0);
    setServers([]);
    setTunnels([]);
  }

  async function saveTheme(next: ThemePrefs) {
    setTheme(next);
    await api('/api/tenant/theme', 'PUT', next);
  }


  // Public Status Page route: /status/<username>
  const [pubData, setPubData] = useState<{ username: string; monitors: { id: number; name: string; status: string; uptime_pct: number; latency_ms: number; history: number[] }[] } | null>(null);
  const statusMatch = window.location.pathname.match(/^\/status\/([^/]+)/);
  useEffect(() => {
    if (statusMatch) {
      api<{ username: string; monitors: { id: number; name: string; status: string; uptime_pct: number; latency_ms: number; history: number[] }[] }>(`/api/public/status/${encodeURIComponent(statusMatch[1])}`)
        .then((r) => {
          if (r.ok) setPubData({ username: r.username, monitors: r.monitors || [] });
        });
    }
  }, []);

  if (statusMatch) {
    return (
      <div className="shell" style={{ maxWidth: 820 }}>
        <div className="card" style={{ marginBottom: 16 }}>
          <h2 style={{ color: 'var(--accent)' }}>{branding.app_name} — Status Page</h2>
          <div style={{ fontSize: '0.82rem', color: 'var(--muted)' }}>Tenant: {pubData?.username || statusMatch[1]}</div>
        </div>
        {(pubData?.monitors || []).map((m) => (
          <div key={m.id} className="card" style={{ marginBottom: 10 }}>
            <div className="toolbar" style={{ marginBottom: 8 }}>
              <strong>{m.name}</strong>
              <div style={{ display: 'flex', gap: 6 }}>
                <span className={`badge ${m.status === 'up' ? 'badge-green' : 'badge-red'}`}>{m.status.toUpperCase()}</span>
                <span className="badge mono">{m.uptime_pct.toFixed(1)}% • {m.latency_ms} ms</span>
              </div>
            </div>
            <div style={{ display: 'flex', gap: 3, height: 16 }}>
              {(m.history || []).map((v, i) => (
                <span key={i} style={{ flex: 1, maxWidth: 14, borderRadius: 4, background: v === 1 ? 'var(--green)' : 'var(--red)' }} />
              ))}
            </div>
          </div>
        ))}
      </div>
    );
  }

  if (loading) {
    return (
      <div className="auth-wrap">
        <div className="card">Loading {branding.app_name}...</div>
      </div>
    );
  }

  if (!initialized) {
    return (
      <div className="auth-wrap">
        <form className="card auth-card" onSubmit={handleInitSetup}>
          <h2 style={{ color: 'var(--accent)', marginBottom: 4 }}>First-Time Setup</h2>
          <p style={{ fontSize: '0.82rem', color: 'var(--muted)', marginBottom: 16 }}>
            Create your commercial SuperAdmin account and isolated multi-tenant SQLite engine.
          </p>
          <div className="field"><label>Brand / Panel Name</label><input value={brandInp} onChange={(e) => setBrandInp(e.target.value)} required /></div>
          <div className="field"><label>SuperAdmin Username</label><input value={uInp} onChange={(e) => setUInp(e.target.value)} placeholder="admin" required /></div>
          <div className="field"><label>SuperAdmin Password</label><input type="password" value={pInp} onChange={(e) => setPInp(e.target.value)} placeholder="Min 6 chars" required /></div>
          <button type="submit" className="btn btn-primary" style={{ width: '100%', marginTop: 8 }}>Launch {brandInp}</button>
        </form>
        {toast && <div className={`toast ${toast.err ? 'err' : ''}`}>{toast.msg}</div>}
      </div>
    );
  }

  if (!user) {
    return (
      <div className="auth-wrap">
        <form className="card auth-card" onSubmit={handleLogin}>
          <h2 style={{ color: 'var(--accent)', marginBottom: 4 }}>{branding.app_name}</h2>
          <p style={{ fontSize: '0.82rem', color: 'var(--muted)', marginBottom: 16 }}>{branding.subtitle}</p>
          <div className="field"><label>Username</label><input value={uInp} onChange={(e) => setUInp(e.target.value)} required /></div>
          <div className="field"><label>Password</label><input type="password" value={pInp} onChange={(e) => setPInp(e.target.value)} required /></div>
          <button type="submit" className="btn btn-primary" style={{ width: '100%', marginTop: 8 }}>Sign In</button>
        </form>
        {toast && <div className={`toast ${toast.err ? 'err' : ''}`}>{toast.msg}</div>}
      </div>
    );
  }

  const isSuper = user.role === 'superadmin' && impersonatedBy === 0;

  return (
    <div className="shell">
      {impersonatedBy > 0 && (
        <div className="imp-banner">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <AlertTriangle size={18} style={{ color: 'var(--yellow)' }} />
            <strong>Impersonating Tenant: {user.username} (Isolated DB: u_{user.id}.db)</strong>
          </div>
          <button className="btn btn-warn btn-sm" onClick={handleExitImpersonate}>Return to SuperAdmin</button>
        </div>
      )}

      <header className="topbar">
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <strong style={{ fontSize: '1.15rem', color: 'var(--accent)' }}>{branding.app_name}</strong>
          <span className="badge badge-green mono">v{version}</span>
          <span className="badge mono">{user.username}</span>
        </div>

        <nav className="nav-pill">
          <button className={`nav-btn ${tab === 'overview' ? 'active' : ''}`} onClick={() => setTab('overview')}>
            <LayoutGrid size={15} /> {theme.lang === 'fa' ? 'داشبورد' : 'Overview'}
          </button>
          <button className={`nav-btn ${tab === 'servers' ? 'active' : ''}`} onClick={() => setTab('servers')}>
            <ServerIcon size={15} /> {theme.lang === 'fa' ? 'سرورها' : 'Servers'}
          </button>
          <button className={`nav-btn ${tab === 'topology' ? 'active' : ''}`} onClick={() => setTab('topology')}>
            <Network size={15} /> {theme.lang === 'fa' ? 'توپولوژی و تانل' : 'Topology'}
          </button>
          <button className={`nav-btn ${tab === 'uptime' ? 'active' : ''}`} onClick={() => setTab('uptime')}>
            <Activity size={15} /> {theme.lang === 'fa' ? 'آپتایم' : 'Uptime'}
          </button>
          <button className={`nav-btn ${tab === 'dns' ? 'active' : ''}`} onClick={() => setTab('dns')}>
            <Globe size={15} /> DNS
          </button>
          <button className={`nav-btn ${tab === 'cloud' ? 'active' : ''}`} onClick={() => setTab('cloud')}>
            <Cloud size={15} /> Cloud
          </button>
          {isSuper && (
            <button className={`nav-btn ${tab === 'admin' ? 'active' : ''}`} onClick={() => setTab('admin')}>
              <Shield size={15} /> SuperAdmin
            </button>
          )}
          <button className={`nav-btn ${tab === 'theme' ? 'active' : ''}`} onClick={() => setTab('theme')}>
            <Palette size={15} /> MDesign
          </button>
        </nav>

        <div style={{ display: 'flex', gap: 6 }}>
          <button
            className="btn btn-sm"
            onClick={() => saveTheme({ ...theme, mode: theme.mode === 'dark' ? 'light' : 'dark' })}
            title="Toggle Dark / Light"
          >
            {theme.mode === 'dark' ? <Sun size={15} /> : <Moon size={15} />}
          </button>
          <button
            className="btn btn-sm mono"
            onClick={() => saveTheme({ ...theme, lang: theme.lang === 'fa' ? 'en' : 'fa' })}
          >
            {theme.lang === 'fa' ? 'EN' : 'FA'}
          </button>
          <button className="btn btn-danger btn-sm" onClick={logout} title="Logout">
            <LogOut size={15} />
          </button>
        </div>
      </header>

      {tab === 'overview' && (
        <div>
          <div className="stats-grid">
            <div className="card">
              <div className="stat-lbl">Your Servers</div>
              <div className="stat-val">{servers.length} / {user.max_servers}</div>
            </div>
            <div className="card">
              <div className="stat-lbl">Your Tunnels</div>
              <div className="stat-val" style={{ color: 'var(--green)' }}>{tunnels.length} / {user.max_tunnels}</div>
            </div>
            <div className="card">
              <div className="stat-lbl">Isolated Storage</div>
              <div className="stat-val mono" style={{ fontSize: '1.15rem', marginTop: 12 }}>tenants/u_{user.id}.db</div>
            </div>
            <div className="card">
              <div className="stat-lbl">Account Status</div>
              <div className="stat-val" style={{ fontSize: '1.15rem', marginTop: 12, color: 'var(--green)' }}>
                {user.role.toUpperCase()} • ACTIVE
              </div>
            </div>
          </div>

          <div className="card">
            <h4 style={{ color: 'var(--accent)', marginBottom: 8 }}>MControl v2 Architecture Ready</h4>
            <p style={{ fontSize: '0.86rem', color: 'var(--muted)', lineHeight: 1.7 }}>
              Your workspace runs inside an isolated per-tenant SQLite WAL database backed by Go 1.23 and a type-safe React 18 MDesign UI.
              Use <strong>Servers</strong> to drag-and-drop cards, <strong>Topology</strong> to wire Entry ⇄ Exit tunnels visually, and <strong>SuperAdmin</strong> to manage commercial tenant plans.
            </p>
          </div>
        </div>
      )}

      {tab === 'servers' && (
        <ServersView
          servers={servers}
          maxServers={user.max_servers}
          onRefresh={loadWorkspace}
          onReorderLocal={setServers}
          notify={notify}
        />
      )}

      {tab === 'topology' && (
        <TopologyView
          servers={servers}
          tunnels={tunnels}
          maxTunnels={user.max_tunnels}
          onRefresh={loadWorkspace}
          notify={notify}
        />
      )}

      {tab === 'uptime' && <UptimeView username={user.username} maxMonitors={user.max_monitors} notify={notify} />}
      {tab === 'dns' && <DNSView notify={notify} />}
      {tab === 'cloud' && <CloudView servers={servers} notify={notify} />}

      {tab === 'admin' && isSuper && (
        <AdminView
          branding={branding}
          onBrandingSaved={setBranding}
          onImpersonated={() => {
            setTab('overview');
            boot();
          }}
          notify={notify}
        />
      )}

      {tab === 'theme' && (
        <div className="card" style={{ maxWidth: 560 }}>
          <h3 style={{ color: 'var(--accent)', marginBottom: 14 }}>MDesign Theme Studio</h3>
          <div className="row2">
            <div className="field">
              <label>Preset</label>
              <select value={theme.preset} onChange={(e) => saveTheme({ ...theme, preset: e.target.value })}>
                <option value="mdesign">MDesign Classic</option>
                <option value="obsidian">Obsidian Gold</option>
                <option value="cyber">Neon Cyber</option>
              </select>
            </div>
            <div className="field">
              <label>Accent Color</label>
              <input type="color" value={theme.accent} onChange={(e) => saveTheme({ ...theme, accent: e.target.value })} style={{ height: 42, padding: 4 }} />
            </div>
          </div>
          <div className="field">
            <label>Corner Radius: {theme.radius}px</label>
            <input type="range" min={8} max={32} value={theme.radius} onChange={(e) => saveTheme({ ...theme, radius: Number(e.target.value) })} />
          </div>
        </div>
      )}

      {toast && <div className={`toast ${toast.err ? 'err' : ''}`}>{toast.msg}</div>}
    </div>
  );
}
