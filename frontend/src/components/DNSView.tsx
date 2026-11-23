import React, { useEffect, useState } from 'react';
import { Globe, Plus, Trash2, ArrowLeftRight } from 'lucide-react';
import { api } from '../api';
import type { DNSRule } from '../types';

interface Props {
  notify: (msg: string, err?: boolean) => void;
}

export function DNSView({ notify }: Props) {
  const [rules, setRules] = useState<DNSRule[]>([]);
  const [provider, setProvider] = useState<'cloudflare' | 'arvancloud'>('cloudflare');
  const [domain, setDomain] = useState('');
  const [zoneId, setZoneId] = useState('');
  const [recordId, setRecordId] = useState('');
  const [recordName, setRecordName] = useState('');
  const [apiToken, setApiToken] = useState('');
  const [primaryIp, setPrimaryIp] = useState('');
  const [backupIp, setBackupIp] = useState('');

  async function load() {
    const r = await api<{ rules: DNSRule[] }>('/api/tenant/dns');
    if (r.ok && r.rules) setRules(r.rules);
  }

  useEffect(() => {
    load();
  }, []);

  async function addRule(e: React.FormEvent) {
    e.preventDefault();
    const r = await api('/api/tenant/dns', 'POST', {
      provider,
      domain,
      zone_id: zoneId,
      record_id: recordId,
      record_name: recordName,
      api_token: apiToken,
      primary_ip: primaryIp,
      backup_ip: backupIp,
    });
    if (r.ok) {
      notify('DNS failover rule added!');
      setDomain('');
      setRecordName('');
      setPrimaryIp('');
      setBackupIp('');
      load();
    } else notify(r.error || 'Failed', true);
  }

  async function switchTarget(id: number, target: 'primary' | 'backup') {
    const r = await api<{ message?: string }>(`/api/tenant/dns/${id}/switch`, 'POST', { target });
    notify(r.message || r.error || 'Switched', !r.ok);
    load();
  }

  async function removeRule(id: number) {
    const r = await api(`/api/tenant/dns/${id}`, 'DELETE');
    if (r.ok) load();
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <h3>DNS Failover (Cloudflare &amp; ArvanCloud)</h3>
      <form className="card" onSubmit={addRule}>
        <div className="row2">
          <div className="field"><label>Provider</label>
            <select value={provider} onChange={(e) => setProvider(e.target.value as 'cloudflare' | 'arvancloud')}>
              <option value="cloudflare">Cloudflare</option>
              <option value="arvancloud">ArvanCloud</option>
            </select>
          </div>
          <div className="field"><label>Root Domain</label><input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="example.com" required /></div>
        </div>
        <div className="row2">
          <div className="field"><label>Record Name (A)</label><input className="mono" value={recordName} onChange={(e) => setRecordName(e.target.value)} placeholder="sub.example.com" required /></div>
          <div className="field"><label>API Token / Key (Encrypted)</label><input type="password" value={apiToken} onChange={(e) => setApiToken(e.target.value)} placeholder="Optional for live sync" /></div>
        </div>
        <div className="row2">
          <div className="field"><label>Zone ID (Cloudflare)</label><input className="mono" value={zoneId} onChange={(e) => setZoneId(e.target.value)} /></div>
          <div className="field"><label>Record ID</label><input className="mono" value={recordId} onChange={(e) => setRecordId(e.target.value)} /></div>
        </div>
        <div className="row2">
          <div className="field"><label>Primary IP</label><input className="mono" value={primaryIp} onChange={(e) => setPrimaryIp(e.target.value)} placeholder="1.1.1.1" required /></div>
          <div className="field"><label>Backup IP</label><input className="mono" value={backupIp} onChange={(e) => setBackupIp(e.target.value)} placeholder="2.2.2.2" /></div>
        </div>
        <button type="submit" className="btn btn-primary btn-sm"><Plus size={14} /> Add DNS Rule</button>
      </form>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {rules.map((r) => (
          <div key={r.id} className="card" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <Globe size={16} style={{ color: 'var(--accent)' }} />
                <strong>{r.record_name}</strong>
                <span className="badge badge-accent">{r.provider.toUpperCase()}</span>
                <span className={`badge ${r.status === 'primary' ? 'badge-green' : 'badge-red'}`}>{r.status.toUpperCase()} ({r.active_ip})</span>
              </div>
              <div className="mono" style={{ fontSize: '0.75rem', color: 'var(--muted)', marginTop: 4 }}>
                Primary: {r.primary_ip} • Backup: {r.backup_ip || '—'} {r.last_switch ? `• Switched: ${r.last_switch}` : ''}
              </div>
            </div>
            <div style={{ display: 'flex', gap: 6 }}>
              <button className="btn btn-primary btn-sm" onClick={() => switchTarget(r.id, r.status === 'primary' ? 'backup' : 'primary')}>
                <ArrowLeftRight size={13} /> Switch to {r.status === 'primary' ? 'Backup' : 'Primary'}
              </button>
              <button className="btn btn-danger btn-sm" onClick={() => removeRule(r.id)}><Trash2 size={13} /></button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
