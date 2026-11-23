import React, { useEffect, useState } from 'react';
import { Shield, UserPlus, LogIn, Trash2, Ban, CheckCircle } from 'lucide-react';
import { api, setToken } from '../api';
import type { Branding, SystemStats, User } from '../types';

interface Props {
  branding: Branding;
  onBrandingSaved: (b: Branding) => void;
  onImpersonated: () => void;
  notify: (msg: string, err?: boolean) => void;
}

export function AdminView({ branding, onBrandingSaved, onImpersonated, notify }: Props) {
  const [users, setUsers] = useState<User[]>([]);
  const [stats, setStats] = useState<SystemStats | null>(null);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [maxServers, setMaxServers] = useState(10);
  const [maxTunnels, setMaxTunnels] = useState(20);
  const [expiresDays, setExpiresDays] = useState(30);

  const [appName, setAppName] = useState(branding.app_name);
  const [subtitle, setSubtitle] = useState(branding.subtitle);
  const [accent, setAccent] = useState(branding.accent);

  async function load() {
    const [ru, rs] = await Promise.all([
      api<{ users: User[] }>('/api/admin/users'),
      api<{ stats: SystemStats }>('/api/admin/overview'),
    ]);
    if (ru.ok && ru.users) setUsers(ru.users);
    if (rs.ok && rs.stats) setStats(rs.stats);
  }

  useEffect(() => {
    load();
  }, []);

  async function createTenant(e: React.FormEvent) {
    e.preventDefault();
    const r = await api('/api/admin/users', 'POST', {
      username,
      password,
      max_servers: maxServers,
      max_tunnels: maxTunnels,
      max_monitors: 20,
      expires_days: expiresDays,
    });
    if (r.ok) {
      notify('Tenant created with isolated SQLite DB!');
      setUsername('');
      setPassword('');
      load();
    } else {
      notify(r.error || 'Failed to create tenant', true);
    }
  }

  async function toggleActive(u: User) {
    const r = await api(`/api/admin/users/${u.id}`, 'PUT', { active: !u.active });
    if (r.ok) {
      notify(u.active ? 'Tenant suspended.' : 'Tenant reactivated.');
      load();
    } else notify(r.error || 'Failed', true);
  }

  async function removeUser(u: User) {
    if (!confirm(`Delete tenant "${u.username}" and permanently wipe their isolated SQLite database?`)) return;
    const r = await api(`/api/admin/users/${u.id}`, 'DELETE');
    if (r.ok) {
      notify('Tenant & isolated DB deleted.');
      load();
    } else notify(r.error || 'Failed', true);
  }

  async function impersonate(u: User) {
    const r = await api<{ token: string }>(`/api/admin/users/${u.id}/impersonate`, 'POST');
    if (r.ok && r.token) {
      setToken(r.token);
      notify(`Switched into ${u.username}'s workspace!`);
      onImpersonated();
    } else notify(r.error || 'Impersonation failed', true);
  }

  async function saveBrand(e: React.FormEvent) {
    e.preventDefault();
    const b: Branding = { app_name: appName, subtitle, accent };
    const r = await api('/api/admin/branding', 'PUT', b);
    if (r.ok) {
      onBrandingSaved(b);
      notify('White-label branding saved!');
    } else notify(r.error || 'Failed', true);
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {stats && (
        <div className="stats-grid" style={{ marginBottom: 0 }}>
          <div className="card"><div className="stat-lbl">Total Accounts</div><div className="stat-val">{stats.total_users}</div></div>
          <div className="card"><div className="stat-lbl">Active Tenants</div><div className="stat-val" style={{ color: 'var(--green)' }}>{stats.active_tenants}</div></div>
          <div className="card"><div className="stat-lbl">Fleet Servers (All DBs)</div><div className="stat-val">{stats.total_servers}</div></div>
          <div className="card"><div className="stat-lbl">Fleet Tunnels (All DBs)</div><div className="stat-val">{stats.total_tunnels}</div></div>
        </div>
      )}

      <form className="card" onSubmit={createTenant}>
        <div className="toolbar">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <UserPlus size={18} style={{ color: 'var(--accent)' }} />
            <h4>Create New Tenant (Isolated SQLite DB)</h4>
          </div>
        </div>
        <div className="row2">
          <div className="field"><label>Username</label><input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="customer01" required /></div>
          <div className="field"><label>Password</label><input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Min 6 chars" required /></div>
        </div>
        <div className="row2">
          <div className="field"><label>Max Servers</label><input type="number" value={maxServers} onChange={(e) => setMaxServers(Number(e.target.value))} min={1} /></div>
          <div className="field"><label>Max Tunnels / Subscription Days (0=unlimited)</label>
            <div className="row2">
              <input type="number" value={maxTunnels} onChange={(e) => setMaxTunnels(Number(e.target.value))} min={1} />
              <input type="number" value={expiresDays} onChange={(e) => setExpiresDays(Number(e.target.value))} min={0} />
            </div>
          </div>
        </div>
        <button type="submit" className="btn btn-primary"><UserPlus size={15} /> Create Isolated Tenant</button>
      </form>

      <div className="card">
        <div className="toolbar">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Shield size={18} style={{ color: 'var(--accent)' }} />
            <h4>Managed Users &amp; Tenants</h4>
          </div>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {users.map((u) => (
            <div
              key={u.id}
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                gap: 10,
                flexWrap: 'wrap',
                padding: '10px 14px',
                borderRadius: 14,
                background: 'var(--row)',
                border: '1px solid var(--border)',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <strong>{u.username}</strong>
                <span className={`badge ${u.role === 'superadmin' ? 'badge-accent' : 'badge-green'}`}>{u.role}</span>
                {!u.active && <span className="badge badge-red">SUSPENDED</span>}
                <span className="mono" style={{ fontSize: '0.74rem', color: 'var(--muted)' }}>
                  DB: u_{u.id}.db • Quota: {u.max_servers} srv / {u.max_tunnels} tun
                  {u.expires_at ? ` • Exp: ${new Date(u.expires_at).toLocaleDateString()}` : ''}
                </span>
              </div>
              {u.role !== 'superadmin' && (
                <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                  <button className="btn btn-primary btn-sm" onClick={() => impersonate(u)}>
                    <LogIn size={13} /> Impersonate
                  </button>
                  <button className="btn btn-warn btn-sm" onClick={() => toggleActive(u)}>
                    {u.active ? <Ban size={13} /> : <CheckCircle size={13} />}
                    {u.active ? 'Suspend' : 'Activate'}
                  </button>
                  <button className="btn btn-danger btn-sm" onClick={() => removeUser(u)}>
                    <Trash2 size={13} />
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      </div>

      <form className="card" onSubmit={saveBrand}>
        <h4 style={{ marginBottom: 10 }}>White-Label Branding</h4>
        <div className="row2">
          <div className="field"><label>Brand Name</label><input value={appName} onChange={(e) => setAppName(e.target.value)} /></div>
          <div className="field"><label>Subtitle</label><input value={subtitle} onChange={(e) => setSubtitle(e.target.value)} /></div>
        </div>
        <div className="field" style={{ maxWidth: 220 }}>
          <label>Default Accent Color</label>
          <input type="color" value={accent} onChange={(e) => setAccent(e.target.value)} style={{ height: 42, padding: 4 }} />
        </div>
        <button type="submit" className="btn btn-primary">Save Branding</button>
      </form>
    </div>
  );
}
