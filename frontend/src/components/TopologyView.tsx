import React, { useEffect, useState } from 'react';
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  type Connection,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { Trash2, Network } from 'lucide-react';
import { api } from '../api';
import type { ServerItem, TunnelItem } from '../types';

interface Props {
  servers: ServerItem[];
  tunnels: TunnelItem[];
  maxTunnels: number;
  onRefresh: () => void;
  notify: (msg: string, err?: boolean) => void;
}

export function TopologyView({ servers, tunnels, maxTunnels, onRefresh, notify }: Props) {
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [pendingConn, setPendingConn] = useState<{ src: number; dst: number } | null>(null);
  const [tunName, setTunName] = useState('');
  const [engine, setEngine] = useState('backhaul');
  const [tunPort, setTunPort] = useState(3080);
  const [ports, setPorts] = useState('443,8443');

  useEffect(() => {
    const nextNodes: Node[] = servers.map((s, idx) => ({
      id: String(s.id),
      position: {
        x: s.pos_x || (s.role === 'iran' ? 80 : 480),
        y: s.pos_y || 50 + idx * 105,
      },
      data: { label: `${s.role === 'iran' ? '🟢' : '🟣'} ${s.name} (${s.host})` },
      style: {
        background: 'var(--card)',
        color: 'var(--text)',
        border: `2px solid ${s.role === 'iran' ? 'var(--green)' : 'var(--accent)'}`,
        borderRadius: '16px',
        padding: '10px 14px',
        fontWeight: 700,
        fontSize: '12px',
      },
    }));

    const nextEdges: Edge[] = tunnels.map((t) => ({
      id: `t-${t.id}`,
      source: String(t.iran_server_id),
      target: String(t.foreign_server_id),
      label: `${t.name} [${t.engine.toUpperCase()}] :${t.tunnel_port}`,
      animated: t.status === 'active',
      style: { stroke: 'var(--accent)', strokeWidth: 2.5 },
    }));

    setNodes(nextNodes);
    setEdges(nextEdges);
  }, [servers, tunnels, setNodes, setEdges]);

  function handleConnect(conn: Connection) {
    if (!conn.source || !conn.target || conn.source === conn.target) return;
    const src = Number(conn.source);
    const dst = Number(conn.target);
    const s1 = servers.find((s) => s.id === src);
    const s2 = servers.find((s) => s.id === dst);
    setTunName(`${s1?.name || src}-to-${s2?.name || dst}`);
    setPendingConn({ src, dst });
  }

  async function handleNodeDragStop(_: React.MouseEvent, node: Node) {
    const r = await api('/api/tenant/layout', 'PUT', {
      items: [
        {
          section: 'topology',
          item_id: node.id,
          sort_index: 0,
          pos_x: Math.round(node.position.x),
          pos_y: Math.round(node.position.y),
        },
      ],
    });
    if (r.ok) notify('Node position saved.');
  }

  async function handleCreateTunnel(e: React.FormEvent) {
    e.preventDefault();
    if (!pendingConn) return;
    const r = await api('/api/tenant/tunnels', 'POST', {
      name: tunName,
      engine,
      iran_server_id: pendingConn.src,
      foreign_server_id: pendingConn.dst,
      tunnel_port: tunPort,
      ports,
    });
    if (r.ok) {
      notify('Tunnel linked on topology canvas!');
      setPendingConn(null);
      onRefresh();
    } else {
      notify(r.error || 'Failed to create tunnel', true);
    }
  }

  async function handleDeleteTunnel(id: number) {
    const r = await api(`/api/tenant/tunnels/${id}`, 'DELETE');
    if (r.ok) {
      notify('Tunnel deleted.');
      onRefresh();
    } else notify(r.error || 'Failed', true);
  }

  return (
    <div>
      <div className="toolbar">
        <div>
          <h3>Interactive Tunnel Topology Canvas</h3>
          <div style={{ fontSize: '0.78rem', color: 'var(--muted)' }}>
            Quota: {tunnels.length} / {maxTunnels} • Drag nodes freely, or drag a wire between two servers to create a tunnel!
          </div>
        </div>
      </div>

      {pendingConn && (
        <form className="card" style={{ marginBottom: 14, borderColor: 'var(--accent)' }} onSubmit={handleCreateTunnel}>
          <div className="toolbar" style={{ marginBottom: 8 }}>
            <strong style={{ color: 'var(--accent)' }}>New Tunnel Connection</strong>
            <button type="button" className="btn btn-sm" onClick={() => setPendingConn(null)}>Cancel</button>
          </div>
          <div className="row2">
            <div className="field">
              <label>Tunnel Name</label>
              <input value={tunName} onChange={(e) => setTunName(e.target.value)} required />
            </div>
            <div className="field">
              <label>Engine</label>
              <select value={engine} onChange={(e) => setEngine(e.target.value)}>
                <option value="backhaul">Backhaul</option>
                <option value="paqet">Paqet</option>
                <option value="rathole">Rathole</option>
                <option value="gre">GRE (IPv4)</option>
                <option value="ip6gre">IP6GRE (IPv6)</option>
                <option value="vxlan">VXLAN</option>
              </select>
            </div>
          </div>
          <div className="row2">
            <div className="field">
              <label>Tunnel Port</label>
              <input type="number" value={tunPort} onChange={(e) => setTunPort(Number(e.target.value))} />
            </div>
            <div className="field">
              <label>Forwarded Ports</label>
              <input className="mono" value={ports} onChange={(e) => setPorts(e.target.value)} />
            </div>
          </div>
          <button type="submit" className="btn btn-primary">Create Tunnel</button>
        </form>
      )}

      <div className="topo-box">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={handleConnect}
          onNodeDragStop={handleNodeDragStop}
          fitView
        >
          <Background />
          <Controls />
          <MiniMap />
        </ReactFlow>
      </div>

      <div className="card">
        <div className="toolbar">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Network size={17} style={{ color: 'var(--accent)' }} />
            <strong>Active Tunnels ({tunnels.length})</strong>
          </div>
        </div>
        {tunnels.length === 0 ? (
          <div style={{ fontSize: '0.82rem', color: 'var(--muted)' }}>
            Drag a connection line between any two server nodes above to create your first tunnel.
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {tunnels.map((t) => (
              <div
                key={t.id}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  padding: '10px 14px',
                  borderRadius: 14,
                  background: 'var(--row)',
                  border: '1px solid var(--border)',
                  gap: 10,
                  flexWrap: 'wrap',
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                  <strong>{t.name}</strong>
                  <span className="badge badge-accent mono">{t.engine.toUpperCase()}</span>
                  <span className="mono" style={{ fontSize: '0.78rem', color: 'var(--muted)' }}>
                    Port :{t.tunnel_port} • Fwd: {t.ports}
                  </span>
                </div>
                <button className="btn btn-danger btn-sm" onClick={() => handleDeleteTunnel(t.id)}>
                  <Trash2 size={14} /> Delete
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
