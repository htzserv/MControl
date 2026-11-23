import React, { useState } from 'react';
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
import { GripVertical, Plus, Trash2, Server as ServerIcon } from 'lucide-react';
import { api } from '../api';
import type { ServerItem } from '../types';

interface CardProps {
  srv: ServerItem;
  onDelete: (id: number) => void;
}

function SortableServerCard({ srv, onDelete }: CardProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: srv.id });
  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.65 : 1,
    zIndex: isDragging ? 20 : 1,
  };

  return (
    <div ref={setNodeRef} style={style} className="card">
      <div className="toolbar" style={{ marginBottom: 10 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span className="drag-handle" {...attributes} {...listeners} title="Drag to reorder">
            <GripVertical size={17} />
          </span>
          <ServerIcon size={17} style={{ color: 'var(--accent)' }} />
          <strong>{srv.name}</strong>
          <span className={`badge ${srv.role === 'iran' ? 'badge-green' : 'badge-accent'}`}>
            {srv.role.toUpperCase()}
          </span>
        </div>
        <button className="btn btn-danger btn-sm" onClick={() => onDelete(srv.id)} title="Delete">
          <Trash2 size={14} />
        </button>
      </div>
      <div className="mono" style={{ fontSize: '0.82rem', color: 'var(--accent)', marginBottom: 6 }}>
        {srv.username}@{srv.host}:{srv.port}
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.76rem', color: 'var(--muted)' }}>
        <span>{srv.country || 'Auto Location'}</span>
        <span className="badge badge-green">{srv.status.toUpperCase()}</span>
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
  const [showAdd, setShowAdd] = useState(false);
  const [name, setName] = useState('');
  const [role, setRole] = useState<'iran' | 'foreign'>('foreign');
  const [host, setHost] = useState('');
  const [port, setPort] = useState(22);
  const [username, setUsername] = useState('root');
  const [secret, setSecret] = useState('');
  const [country, setCountry] = useState('');

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  );

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
    else notify(r.error || 'Failed to save order', true);
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    const r = await api('/api/tenant/servers', 'POST', { name, role, host, port, username, secret, country });
    if (r.ok) {
      notify('Server added to your isolated workspace!');
      setName('');
      setHost('');
      setSecret('');
      setCountry('');
      setShowAdd(false);
      onRefresh();
    } else {
      notify(r.error || 'Failed to add server', true);
    }
  }

  async function handleDelete(id: number) {
    if (!confirm('Remove this server and its linked tunnels?')) return;
    const r = await api(`/api/tenant/servers/${id}`, 'DELETE');
    if (r.ok) {
      notify('Server removed.');
      onRefresh();
    } else {
      notify(r.error || 'Failed to delete', true);
    }
  }

  return (
    <div>
      <div className="toolbar">
        <div>
          <h3>Servers (Drag &amp; Drop Cards)</h3>
          <div style={{ fontSize: '0.78rem', color: 'var(--muted)' }}>
            Quota: {servers.length} / {maxServers} • Grab the handle to reorder cards persistently.
          </div>
        </div>
        <button className="btn btn-primary" onClick={() => setShowAdd(!showAdd)}>
          <Plus size={16} /> Add Server
        </button>
      </div>

      {showAdd && (
        <form className="card" style={{ marginBottom: 16 }} onSubmit={handleCreate}>
          <div className="row2">
            <div className="field">
              <label>Server Name</label>
              <input value={name} onChange={(e) => setName(e.target.value)} placeholder="DE-Hetzner-01" required />
            </div>
            <div className="field">
              <label>Role</label>
              <select value={role} onChange={(e) => setRole(e.target.value as 'iran' | 'foreign')}>
                <option value="iran">Entry (Iran)</option>
                <option value="foreign">Exit (Foreign)</option>
              </select>
            </div>
          </div>
          <div className="row2">
            <div className="field">
              <label>IP / Hostname</label>
              <input className="mono" value={host} onChange={(e) => setHost(e.target.value)} placeholder="1.2.3.4" required />
            </div>
            <div className="field">
              <label>SSH Port &amp; User</label>
              <div className="row2">
                <input type="number" value={port} onChange={(e) => setPort(Number(e.target.value))} />
                <input className="mono" value={username} onChange={(e) => setUsername(e.target.value)} />
              </div>
            </div>
          </div>
          <div className="row2">
            <div className="field">
              <label>SSH Password (Encrypted AES-GCM)</label>
              <input type="password" value={secret} onChange={(e) => setSecret(e.target.value)} placeholder="••••••••" />
            </div>
            <div className="field">
              <label>Location / Datacenter</label>
              <input value={country} onChange={(e) => setCountry(e.target.value)} placeholder="Germany • Hetzner" />
            </div>
          </div>
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
            <button type="button" className="btn" onClick={() => setShowAdd(false)}>Cancel</button>
            <button type="submit" className="btn btn-primary">Save Server</button>
          </div>
        </form>
      )}

      {servers.length === 0 ? (
        <div className="card" style={{ textAlign: 'center', color: 'var(--muted)', padding: 34 }}>
          No servers yet. Add an Entry (Iran) and Exit (Foreign) server to build tunnels visually.
        </div>
      ) : (
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
          <SortableContext items={servers.map((s) => s.id)} strategy={rectSortingStrategy}>
            <div className="dnd-grid">
              {servers.map((srv) => (
                <SortableServerCard key={srv.id} srv={srv} onDelete={handleDelete} />
              ))}
            </div>
          </SortableContext>
        </DndContext>
      )}
    </div>
  );
}
