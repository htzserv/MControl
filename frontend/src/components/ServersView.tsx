import React, { useEffect, useState } from 'react';
import {
  DndContext,
  closestCenter,
  PointerSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  rectSortingStrategy,
  useSortable,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import {
  GripVertical,
  Plus,
  Trash2,
  Server as ServerIcon,
  RefreshCw,
  Terminal,
  FolderOpen,
  Gauge,
  Edit3,
  Zap,
} from 'lucide-react';
import { api } from '../api';
import { ServerOpsModal, type OpsTab } from './ServerOpsModal';
import type { ServerItem } from '../types';

interface CardProps {
  srv: ServerItem;
  tweakState?: { ipv6: boolean; bbr: boolean; fw: boolean };
  probing: boolean;
  onProbe: (id: number) => void;
  onSpeedtest: (id: number) => void;
  onTweak: (id: number, action: string) => void;
  onOpenOps: (srv: ServerItem, tab: OpsTab) => void;
  onEdit: (srv: ServerItem) => void;
  onDelete: (id: number) => void;
}

function SortableServerCard({
  srv,
  tweakState,
  probing,
  onProbe,
  onSpeedtest,
  onTweak,
  onOpenOps,
  onEdit,
  onDelete,
}: CardProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: srv.id });
  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.65 : 1,
    zIndex: isDragging ? 20 : 1,
  };

  const online = srv.status === 'online';

  return (
    <div ref={setNodeRef} style={style} className="card">
      <div className="toolbar" style={{ marginBottom: 8 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <span className="drag-handle" {...attributes} {...listeners} title="Drag to reorder">
            <GripVertical size={17} />
          </span>
          <ServerIcon size={17} style={{ color: 'var(--accent)' }} />
          <strong>{srv.name}</strong>
          <span className={`badge ${srv.role === 'iran' ? 'badge-green' : 'badge-accent'}`}>
            {srv.role.toUpperCase()}
          </span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          {srv.ping_ms && <span className="badge badge-green mono">{srv.ping_ms}</span>}
          <span className={`badge ${online ? 'badge-green' : 'badge-red'}`}>{srv.status.toUpperCase()}</span>
        </div>
      </div>

      <div className="mono" style={{ fontSize: '0.82rem', color: 'var(--accent)', marginBottom: 6 }}>
        {srv.username}@{srv.host}:{srv.port}
      </div>

      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.76rem', color: 'var(--muted)', flexWrap: 'wrap', gap: 6 }}>
        <span>{srv.country || 'Auto GeoIP'} {srv.datacenter ? `• ${srv.datacenter}` : ''}</span>
        <span className="mono">CPU {srv.cpu || '0%'} • RAM {srv.ram || '0%'}</span>
      </div>

      {srv.last_speed && (
        <div className="mono" style={{ fontSize: '0.75rem', color: 'var(--green)', marginTop: 4 }}>
          {srv.last_speed}
        </div>
      )}
      {srv.last_error && (
        <div style={{ fontSize: '0.74rem', color: 'var(--red)', marginTop: 4 }}>{srv.last_error}</div>
      )}

      {/* Centered Live Tweak Bubbles */}
      <div className="tweak-bar">
        <button
          type="button"
          className={`tweak-pill ${tweakState ? (tweakState.ipv6 ? 'on' : 'off') : ''}`}
          onClick={() => onTweak(srv.id, tweakState?.ipv6 ? 'ipv6_off' : 'ipv6_on')}
        >
          IPv6 {tweakState ? (tweakState.ipv6 ? 'ON' : 'OFF') : ''}
        </button>
        <button
          type="button"
          className={`tweak-pill ${tweakState ? (tweakState.bbr ? 'on' : 'off') : ''}`}
          onClick={() => onTweak(srv.id, tweakState?.bbr ? 'bbr_off' : 'bbr_on')}
        >
          BBR {tweakState ? (tweakState.bbr ? 'ON' : 'OFF') : ''}
        </button>
        <button
          type="button"
          className={`tweak-pill ${tweakState ? (tweakState.fw ? 'on' : 'off') : ''}`}
          onClick={() => onTweak(srv.id, tweakState?.fw ? 'fw_off' : 'fw_on')}
        >
          Firewall {tweakState ? (tweakState.fw ? 'ON' : 'OFF') : ''}
        </button>
        <button type="button" className="tweak-pill" onClick={() => onTweak(srv.id, 'net_opt')}>
          <Zap size={12} /> Optimize
        </button>
        <button type="button" className="tweak-pill" onClick={() => onTweak(srv.id, 'clear_cache')}>
          Clean RAM
        </button>
      </div>

      {/* Centered Action Buttons */}
      <div className="card-ops">
        <button className="btn btn-primary btn-sm" onClick={() => onOpenOps(srv, 'terminal')}>
          <Terminal size={13} /> Terminal
        </button>
        <button className="btn btn-sm" onClick={() => onOpenOps(srv, 'sftp')}>
          <FolderOpen size={13} /> SFTP
        </button>
        <button className="btn btn-sm" disabled={probing} onClick={() => onProbe(srv.id)}>
          <RefreshCw size={13} /> Probe
        </button>
        <button className="btn btn-sm" onClick={() => onSpeedtest(srv.id)}>
          <Gauge size={13} /> Speed
        </button>
        <button className="btn btn-sm" onClick={() => onEdit(srv)}>
          <Edit3 size={13} /> Edit
        </button>
        <button className="btn btn-danger btn-sm" onClick={() => onDelete(srv.id)}>
          <Trash2 size={13} />
        </button>
      </div>
    </div>
  );
}

interface Props {
  servers: ServerItem[];
  maxServers: number;
  onRefresh: () => void;
  onReorderLocal: (next: ServerItem[]) => void;
  notify: (msg: string, err?: boolean) => void;
}

export function ServersView({ servers, maxServers, onRefresh, onReorderLocal, notify }: Props) {
  const [showForm, setShowForm] = useState(false);
  const [editId, setEditId] = useState<number>(0);
  const [name, setName] = useState('');
  const [role, setRole] = useState<'iran' | 'foreign'>('foreign');
  const [host, setHost] = useState('');
  const [port, setPort] = useState(22);
  const [username, setUsername] = useState('root');
  const [authType, setAuthType] = useState<'password' | 'key'>('password');
  const [secret, setSecret] = useState('');
  const [country, setCountry] = useState('');

  const [tweaks, setTweaks] = useState<Record<number, { ipv6: boolean; bbr: boolean; fw: boolean }>>({});
  const [probingIds, setProbingIds] = useState<Record<number, boolean>>({});
  const [opsModal, setOpsModal] = useState<{ srv: ServerItem; tab: OpsTab } | null>(null);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  );

  useEffect(() => {
    servers.forEach((s) => {
      if (s.status === 'online' && !tweaks[s.id]) {
        api<{ state?: { ipv6: boolean; bbr: boolean; fw: boolean } }>(`/api/tenant/servers/${s.id}/tweak`, 'POST', {
          action: 'status',
        }).then((r) => {
          if (r.ok && r.state) {
            setTweaks((prev) => ({ ...prev, [s.id]: r.state! }));
          }
        });
      }
    });
  }, [servers]);

  async function handleDragEnd(event: DragEndEvent) {
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const oldIdx = servers.findIndex((s) => s.id === active.id);
    const newIdx = servers.findIndex((s) => s.id === over.id);
    if (oldIdx < 0 || newIdx < 0) return;

    const reordered = arrayMove(servers, oldIdx, newIdx);
    onReorderLocal(reordered);

    const items = reordered.map((s, idx) => ({
      section: 'servers',
      item_id: String(s.id),
      sort_index: idx,
      pos_x: s.pos_x,
      pos_y: s.pos_y,
    }));
    const r = await api('/api/tenant/layout', 'PUT', { items });
    if (r.ok) notify('Card order saved!');
  }

  function openNewForm() {
    setEditId(0);
    setName('');
    setRole('foreign');
    setHost('');
    setPort(22);
    setUsername('root');
    setAuthType('password');
    setSecret('');
    setCountry('');
    setShowForm(true);
  }

  function openEditForm(s: ServerItem) {
    setEditId(s.id);
    setName(s.name);
    setRole(s.role);
    setHost(s.host);
    setPort(s.port);
    setUsername(s.username);
    setAuthType(s.auth_type || 'password');
    setSecret('');
    setCountry(s.country);
    setShowForm(true);
  }

  async function handleSaveServer(e: React.FormEvent) {
    e.preventDefault();
    const payload = { name, role, host, port, username, auth_type: authType, secret, country };
    const r = editId
      ? await api(`/api/tenant/servers/${editId}`, 'PUT', payload)
      : await api('/api/tenant/servers', 'POST', payload);
    if (r.ok) {
      notify(editId ? 'Server updated!' : 'Server added!');
      setShowForm(false);
      onRefresh();
    } else {
      notify(r.error || 'Failed to save server', true);
    }
  }

  async function handleProbe(id: number) {
    setProbingIds((p) => ({ ...p, [id]: true }));
    notify('Probing server via SSH...');
    try {
      const r = await api(`/api/tenant/servers/${id}/probe`, 'POST');
      if (r.ok) {
        notify('Probe complete!');
        onRefresh();
      } else notify(r.error || 'Probe failed', true);
    } finally {
      setProbingIds((p) => ({ ...p, [id]: false }));
    }
  }

  async function handleProbeAll() {
    notify('Probing all servers in parallel...');
    const r = await api('/api/tenant/servers/probe-all', 'POST');
    if (r.ok) {
      notify('All servers probed!');
      onRefresh();
    }
  }

  async function handleSpeedtest(id: number) {
    notify('Running Speedtest over SSH (~15s)...');
    const r = await api<{ speed?: string }>(`/api/tenant/servers/${id}/speedtest`, 'POST');
    if (r.ok) {
      notify(`Speedtest: ${r.speed}`);
      onRefresh();
    } else notify(r.error || 'Speedtest failed', true);
  }

  async function handleTweak(id: number, action: string) {
    notify(`Applying ${action}...`);
    const r = await api<{ output?: string }>(`/api/tenant/servers/${id}/tweak`, 'POST', { action });
    if (r.ok) {
      notify(r.output || 'Done!');
      const st = await api<{ state?: { ipv6: boolean; bbr: boolean; fw: boolean } }>(`/api/tenant/servers/${id}/tweak`, 'POST', {
        action: 'status',
      });
      if (st.ok && st.state) setTweaks((prev) => ({ ...prev, [id]: st.state! }));
    } else notify(r.error || 'Tweak failed', true);
  }

  async function handleDelete(id: number) {
    if (!confirm('Remove this server and its linked tunnels?')) return;
    const r = await api(`/api/tenant/servers/${id}`, 'DELETE');
    if (r.ok) {
      notify('Server removed.');
      onRefresh();
    }
  }

  return (
    <div>
      <div className="toolbar">
        <div>
          <h3>Servers &amp; Live SSH Fleet</h3>
          <div style={{ fontSize: '0.78rem', color: 'var(--muted)' }}>
            Quota: {servers.length} / {maxServers} • Drag cards to reorder • Click Terminal or SFTP for full workspace.
          </div>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="btn" onClick={handleProbeAll}>
            <RefreshCw size={15} /> Probe All (SSH)
          </button>
          <button className="btn btn-primary" onClick={openNewForm}>
            <Plus size={16} /> Add Server
          </button>
        </div>
      </div>

      {showForm && (
        <form className="card" style={{ marginBottom: 16 }} onSubmit={handleSaveServer}>
          <h4 style={{ marginBottom: 10, color: 'var(--accent)' }}>{editId ? 'Edit Server' : 'Add New Server'}</h4>
          <div className="row2">
            <div className="field"><label>Server Name</label><input value={name} onChange={(e) => setName(e.target.value)} placeholder="DE-Hetzner-01" required /></div>
            <div className="field"><label>Role</label>
              <select value={role} onChange={(e) => setRole(e.target.value as 'iran' | 'foreign')}>
                <option value="iran">Entry (Iran)</option>
                <option value="foreign">Exit (Foreign)</option>
              </select>
            </div>
          </div>
          <div className="row2">
            <div className="field"><label>IP / Hostname</label><input className="mono" value={host} onChange={(e) => setHost(e.target.value)} placeholder="1.2.3.4" required /></div>
            <div className="field"><label>SSH Port &amp; User</label>
              <div className="row2">
                <input type="number" value={port} onChange={(e) => setPort(Number(e.target.value))} />
                <input className="mono" value={username} onChange={(e) => setUsername(e.target.value)} />
              </div>
            </div>
          </div>
          <div className="row2">
            <div className="field"><label>Auth Type</label>
              <select value={authType} onChange={(e) => setAuthType(e.target.value as 'password' | 'key')}>
                <option value="password">Password</option>
                <option value="key">SSH Private Key (PEM)</option>
              </select>
            </div>
            <div className="field"><label>{editId ? 'New Secret (blank = keep)' : 'SSH Password / Key'}</label>
              <input type="password" value={secret} onChange={(e) => setSecret(e.target.value)} placeholder="••••••••" />
            </div>
          </div>
          <div className="field"><label>Location (blank = Auto GeoIP)</label><input value={country} onChange={(e) => setCountry(e.target.value)} placeholder="Germany" /></div>
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
            <button type="button" className="btn" onClick={() => setShowForm(false)}>Cancel</button>
            <button type="submit" className="btn btn-primary">{editId ? 'Save Changes' : 'Add Server'}</button>
          </div>
        </form>
      )}

      {servers.length === 0 ? (
        <div className="card" style={{ textAlign: 'center', color: 'var(--muted)', padding: 34 }}>
          No servers yet. Click <strong>Add Server</strong> to connect your first node over SSH.
        </div>
      ) : (
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
          <SortableContext items={servers.map((s) => s.id)} strategy={rectSortingStrategy}>
            <div className="dnd-grid">
              {servers.map((srv) => (
                <SortableServerCard
                  key={srv.id}
                  srv={srv}
                  tweakState={tweaks[srv.id]}
                  probing={!!probingIds[srv.id]}
                  onProbe={handleProbe}
                  onSpeedtest={handleSpeedtest}
                  onTweak={handleTweak}
                  onOpenOps={(s, tab) => setOpsModal({ srv: s, tab })}
                  onEdit={openEditForm}
                  onDelete={handleDelete}
                />
              ))}
            </div>
          </SortableContext>
        </DndContext>
      )}

      {opsModal && (
        <ServerOpsModal
          server={opsModal.srv}
          initialTab={opsModal.tab}
          onClose={() => setOpsModal(null)}
          notify={notify}
        />
      )}
    </div>
  );
}
