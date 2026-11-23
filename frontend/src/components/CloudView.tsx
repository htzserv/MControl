import React, { useEffect, useState } from 'react';
import { Cloud, Plus, Trash2, RefreshCw } from 'lucide-react';
import { api } from '../api';
import type { CloudAccount, CloudInstance, ServerItem } from '../types';

interface Props {
  servers: ServerItem[];
  notify: (msg: string, err?: boolean) => void;
}

export function CloudView({ servers, notify }: Props) {
  const [spSrv, setSpSrv] = useState<number>(servers[0]?.id || 0);
  const [spPort, setSpPort] = useState(10808);
  const [spUser, setSpUser] = useState('mcontrol');
  const [spPass, setSpPass] = useState('');
  const [spUrl, setSpUrl] = useState('');
  const [spBusy, setSpBusy] = useState(false);
  const [accounts, setAccounts] = useState<CloudAccount[]>([]);
  const [instances, setInstances] = useState<CloudInstance[]>([]);
  const [provider, setProvider] = useState<'hetzner' | 'digitalocean' | 'vultr'>('hetzner');
  const [label, setLabel] = useState('');
  const [apiToken, setApiToken] = useState('');

  async function load() {
    const r = await api<{ accounts: CloudAccount[] }>('/api/tenant/cloud');
    if (r.ok && r.accounts) setAccounts(r.accounts);
  }

  useEffect(() => {
    load();
  }, []);

  async function addAccount(e: React.FormEvent) {
    e.preventDefault();
    const r = await api('/api/tenant/cloud', 'POST', { provider, label, api_token: apiToken });
    if (r.ok) {
      notify('Cloud provider API saved (AES-GCM encrypted)!');
      setLabel('');
      setApiToken('');
      load();
    } else notify(r.error || 'Failed', true);
  }

  async function fetchInstances(id: number) {
    notify('Fetching live cloud instances...');
    const r = await api<{ instances: CloudInstance[] }>(`/api/tenant/cloud/${id}/instances`);
    if (r.ok) {
      setInstances(r.instances || []);
      notify(`Loaded ${(r.instances || []).length} instances.`);
    } else notify(r.error || 'Failed to load instances', true);
  }

  async function removeAcc(id: number) {
    const r = await api(`/api/tenant/cloud/${id}`, 'DELETE');
    if (r.ok) load();
  }


  async function deploySwiftProxy(e: React.FormEvent) {
    e.preventDefault();
    const targetId = spSrv || servers[0]?.id || 0;
    if (!targetId) {
      notify('Add a server first in the Servers tab.', true);
      return;
    }
    setSpBusy(true);
    notify('Deploying SwiftProxy (SOCKS5/HTTP) over SSH...');
    try {
      const r = await api<{ proxy_url?: string; output?: string }>('/api/tenant/cloud/swiftproxy', 'POST', {
        server_id: targetId,
        port: spPort,
        username: spUser,
        password: spPass,
      });
      if (r.ok && r.proxy_url) {
        setSpUrl(r.proxy_url);
        notify('SwiftProxy deployed & active!');
      } else notify(r.error || 'Deploy failed', true);
    } finally {
      setSpBusy(false);
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <h3>Cloud &amp; Datacenter Providers (Hetzner / DigitalOcean / Vultr)</h3>
      <form className="card" onSubmit={addAccount}>
        <div className="row2">
          <div className="field"><label>Provider</label>
            <select value={provider} onChange={(e) => setProvider(e.target.value as 'hetzner' | 'digitalocean' | 'vultr')}>
              <option value="hetzner">Hetzner Cloud</option>
              <option value="digitalocean">DigitalOcean</option>
              <option value="vultr">Vultr</option>
            </select>
          </div>
          <div className="field"><label>Account Label</label><input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="My Hetzner Project" required /></div>
        </div>
        <div className="field"><label>API Token (Encrypted)</label><input type="password" value={apiToken} onChange={(e) => setApiToken(e.target.value)} required /></div>
        <button type="submit" className="btn btn-primary btn-sm"><Plus size={14} /> Connect Provider</button>
      </form>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {accounts.map((a) => (
          <div key={a.id} className="card" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <Cloud size={16} style={{ color: 'var(--accent)' }} />
              <strong>{a.label}</strong>
              <span className="badge badge-accent">{a.provider.toUpperCase()}</span>
            </div>
            <div style={{ display: 'flex', gap: 6 }}>
              <button className="btn btn-primary btn-sm" onClick={() => fetchInstances(a.id)}><RefreshCw size={13} /> List Servers</button>
              <button className="btn btn-danger btn-sm" onClick={() => removeAcc(a.id)}><Trash2 size={13} /></button>
            </div>
          </div>
        ))}
      </div>

      {instances.length > 0 && (
        <div className="card">
          <h4 style={{ marginBottom: 10 }}>Live Cloud Instances ({instances.length})</h4>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {instances.map((i) => (
              <div key={i.id} className="file-row" style={{ cursor: 'default' }}>
                <div>
                  <strong>{i.name}</strong> <span className="badge badge-accent">{i.provider}</span>{' '}
                  <span className="mono" style={{ fontSize: '0.78rem', color: 'var(--accent)' }}>{i.ipv4}</span>
                </div>
                <div style={{ display: 'flex', gap: 6 }}>
                  <span className="badge">{i.region}</span>
                  <span className="badge badge-green">{i.status}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
      <form className="card" onSubmit={deploySwiftProxy}>
        <h4 style={{ marginBottom: 10, color: 'var(--accent)' }}>SwiftProxy (1-Click Authenticated SOCKS5/HTTP)</h4>
        <div className="row2">
          <div className="field"><label>Target Server</label>
            <select value={spSrv || servers[0]?.id || 0} onChange={(e) => setSpSrv(Number(e.target.value))}>
              {servers.map((s) => <option key={s.id} value={s.id}>{s.name} ({s.host})</option>)}
            </select>
          </div>
          <div className="field"><label>Port</label><input type="number" value={spPort} onChange={(e) => setSpPort(Number(e.target.value))} /></div>
        </div>
        <div className="row2">
          <div className="field"><label>Proxy Username</label><input className="mono" value={spUser} onChange={(e) => setSpUser(e.target.value)} required /></div>
          <div className="field"><label>Proxy Password</label><input type="password" value={spPass} onChange={(e) => setSpPass(e.target.value)} required /></div>
        </div>
        <button type="submit" className="btn btn-primary btn-sm" disabled={spBusy}>
          {spBusy ? 'Deploying...' : 'Deploy SwiftProxy'}
        </button>
        {spUrl && (
          <div className="mono" style={{ marginTop: 10, padding: '8px 12px', borderRadius: 12, background: 'var(--row)', color: 'var(--green)', fontSize: '0.8rem', wordBreak: 'break-all' }}>
            {spUrl}
          </div>
        )}
      </form>
    </div>
  );
}
