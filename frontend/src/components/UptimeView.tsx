import React, { useEffect, useState } from 'react';
import { Activity, Plus, Trash2, RefreshCw, Send, ExternalLink } from 'lucide-react';
import { api } from '../api';
import type { MonitorItem, TelegramConfig } from '../types';

interface Props {
  username: string;
  maxMonitors: number;
  notify: (msg: string, err?: boolean) => void;
}

export function UptimeView({ username, maxMonitors, notify }: Props) {
  const [monitors, setMonitors] = useState<MonitorItem[]>([]);
  const [name, setName] = useState('');
  const [type, setType] = useState<'http' | 'tcp'>('http');
  const [target, setTarget] = useState('');
  const [isPublic, setIsPublic] = useState(true);

  const [tg, setTg] = useState<TelegramConfig>({ enabled: false, bot_token: '', chat_id: '' });

  async function load() {
    const [rm, rt] = await Promise.all([
      api<{ monitors: MonitorItem[] }>('/api/tenant/monitors'),
      api<{ telegram: TelegramConfig }>('/api/tenant/telegram'),
    ]);
    if (rm.ok && rm.monitors) setMonitors(rm.monitors);
    if (rt.ok && rt.telegram) setTg(rt.telegram);
  }

  useEffect(() => {
    load();
  }, []);

  async function addMonitor(e: React.FormEvent) {
    e.preventDefault();
    const r = await api('/api/tenant/monitors', 'POST', { name, type, target, interval_sec: 60, public: isPublic });
    if (r.ok) {
      notify('Monitor added & checked!');
      setName('');
      setTarget('');
      load();
    } else notify(r.error || 'Failed', true);
  }

  async function checkNow(id: number) {
    const r = await api<{ up?: boolean; latency_ms?: number }>(`/api/tenant/monitors/${id}/check`, 'POST');
    if (r.ok) {
      notify(r.up ? `UP (${r.latency_ms} ms)` : 'DOWN!', !r.up);
      load();
    }
  }

  async function removeMonitor(id: number) {
    const r = await api(`/api/tenant/monitors/${id}`, 'DELETE');
    if (r.ok) load();
  }

  async function saveTelegram(e: React.FormEvent) {
    e.preventDefault();
    const r = await api('/api/tenant/telegram', 'PUT', tg);
    notify(r.ok ? 'Telegram alerts saved!' : r.error || 'Failed', !r.ok);
  }

  async function testTelegram() {
    const r = await api<{ message?: string }>('/api/tenant/telegram/test', 'POST');
    notify(r.message || r.error || 'Done', !r.ok);
  }

  const publicUrl = `${window.location.origin}/status/${encodeURIComponent(username)}`;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div className="toolbar" style={{ marginBottom: 0 }}>
        <div>
          <h3>Uptime Monitoring &amp; Isolated Telegram Alerts</h3>
          <div style={{ fontSize: '0.78rem', color: 'var(--muted)' }}>
            Quota: {monitors.length} / {maxMonitors} • Public Status Page:{' '}
            <a href={publicUrl} target="_blank" rel="noreferrer" style={{ color: 'var(--accent)' }}>
              /status/{username} <ExternalLink size={12} />
            </a>
          </div>
        </div>
        <button className="btn btn-sm" onClick={load}><RefreshCw size={14} /> Refresh</button>
      </div>

      <form className="card" onSubmit={addMonitor}>
        <div className="row2">
          <div className="field"><label>Monitor Name</label><input value={name} onChange={(e) => setName(e.target.value)} placeholder="Main Website / VPN Entry" required /></div>
          <div className="field"><label>Type</label>
            <select value={type} onChange={(e) => setType(e.target.value as 'http' | 'tcp')}>
              <option value="http">HTTP / HTTPS URL</option>
              <option value="tcp">TCP Port (host:port)</option>
            </select>
          </div>
        </div>
        <div className="field"><label>Target</label><input className="mono" value={target} onChange={(e) => setTarget(e.target.value)} placeholder={type === 'http' ? 'https://example.com' : '1.2.3.4:443'} required /></div>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 8 }}>
          <label style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: '0.82rem' }}>
            <input type="checkbox" style={{ width: 'auto' }} checked={isPublic} onChange={(e) => setIsPublic(e.target.checked)} />
            Show on public /status/{username} page
          </label>
          <button type="submit" className="btn btn-primary btn-sm"><Plus size={14} /> Add Monitor</button>
        </div>
      </form>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {monitors.map((m) => (
          <div key={m.id} className="card">
            <div className="toolbar" style={{ marginBottom: 8 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <Activity size={16} style={{ color: m.status === 'up' ? 'var(--green)' : 'var(--red)' }} />
                <strong>{m.name}</strong>
                <span className={`badge ${m.status === 'up' ? 'badge-green' : 'badge-red'}`}>{m.status.toUpperCase()}</span>
                <span className="badge mono">{m.uptime_pct.toFixed(1)}%</span>
                <span className="mono" style={{ fontSize: '0.76rem', color: 'var(--muted)' }}>{m.latency_ms} ms • {m.target}</span>
              </div>
              <div style={{ display: 'flex', gap: 6 }}>
                <button className="btn btn-sm" onClick={() => checkNow(m.id)}><RefreshCw size={13} /> Check</button>
                <button className="btn btn-danger btn-sm" onClick={() => removeMonitor(m.id)}><Trash2 size={13} /></button>
              </div>
            </div>
            <div style={{ display: 'flex', gap: 3, height: 18, alignItems: 'center' }}>
              {(m.history || []).map((v, idx) => (
                <span
                  key={idx}
                  style={{
                    flex: 1,
                    maxWidth: 14,
                    height: '100%',
                    borderRadius: 4,
                    background: v === 1 ? 'var(--green)' : 'var(--red)',
                    opacity: 0.88,
                  }}
                />
              ))}
            </div>
            {m.last_error && <div style={{ fontSize: '0.74rem', color: 'var(--red)', marginTop: 6 }}>{m.last_error}</div>}
          </div>
        ))}
      </div>

      <form className="card" onSubmit={saveTelegram}>
        <h4 style={{ marginBottom: 10, color: 'var(--accent)' }}>Per-User Telegram Bot Alerts</h4>
        <div className="row2">
          <div className="field"><label>Bot Token (Encrypted)</label><input type="password" value={tg.bot_token} onChange={(e) => setTg({ ...tg, bot_token: e.target.value })} placeholder="123456:ABC-DEF..." /></div>
          <div className="field"><label>Chat ID</label><input className="mono" value={tg.chat_id} onChange={(e) => setTg({ ...tg, chat_id: e.target.value })} placeholder="-100123456789" /></div>
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 8 }}>
          <label style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: '0.82rem' }}>
            <input type="checkbox" style={{ width: 'auto' }} checked={tg.enabled} onChange={(e) => setTg({ ...tg, enabled: e.target.checked })} />
            Enable instant DOWN / RECOVERED alerts
          </label>
          <div style={{ display: 'flex', gap: 8 }}>
            <button type="button" className="btn btn-sm" onClick={testTelegram}><Send size={13} /> Test</button>
            <button type="submit" className="btn btn-primary btn-sm">Save Bot</button>
          </div>
        </div>
      </form>
    </div>
  );
}
