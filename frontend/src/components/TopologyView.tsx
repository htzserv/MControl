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
  type OnNodeDrag,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import {
  Trash2,
  Network,
  Rocket,
  Activity,
  RotateCw,
  Square,
  FileCode2,
  Gauge,
  Route,
  Terminal,
} from 'lucide-react';
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

  // Tunnel creation form
  const [tunName, setTunName] = useState('');
  const [engine, setEngine] = useState('backhaul');
  const [transport, setTransport] = useState('tcp');
  const [tunPort, setTunPort] = useState(3080);
  const [ports, setPorts] = useState('443,8443');
  const [mtu, setMtu] = useState(1420);
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [busyId, setBusyId] = useState<number>(0);

  // Modal output for logs / MikroTik script
  const [viewerModal, setViewerModal] = useState<{ title: string; text: string } | null>(null);

  // Network Diagnostics (Path MTU + MTR)
  const [diagSrc, setDiagSrc] = useState<number>(0);
  const [diagDst, setDiagDst] = useState<string>('');
  const [diagOut, setDiagOut] = useState<string>('');
  const [diagBusy, setDiagBusy] = useState(false);

  useEffect(() => {
    if (servers.length > 0 && diagSrc === 0) {
      setDiagSrc(servers[0].id);
      if (servers.length > 1) setDiagDst(servers[1].host);
    }
  }, [servers, diagSrc]);

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
      label: `${t.name} [${t.engine.toUpperCase()}] :${t.tunnel_port}${t.last_ping ? ` • ${t.last_ping}` : ''}`,
      animated: t.status === 'active',
      style: {
        stroke: t.status === 'active' ? 'var(--green)' : t.status === 'error' ? 'var(--red)' : 'var(--accent)',
        strokeWidth: 2.5,
      },
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

  const handleNodeDragStop: OnNodeDrag = async (_, node) => {
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
  };

  async function handleCreateTunnel(e: React.FormEvent) {
    e.preventDefault();
    if (!pendingConn) return;
    const r = await api<{ id: number }>('/api/tenant/tunnels', 'POST', {
      name: tunName,
      engine,
      transport,
      iran_server_id: pendingConn.src,
      foreign_server_id: pendingConn.dst,
      tunnel_port: tunPort,
      ports,
      mtu,
    });
    if (r.ok && r.id) {
      setPendingConn(null);
      onRefresh();
      if (autoDeploy) {
        notify('Tunnel created! Deploying to both servers over SSH...');
        await deployTunnel(r.id);
      } else {
        notify('Tunnel saved as draft.');
      }
    } else {
      notify(r.error || 'Failed to create tunnel', true);
    }
  }

  async function deployTunnel(id: number) {
    setBusyId(id);
    notify('Deploying tunnel over SSH (Entry + Exit)...');
    try {
      const r = await api<{ status?: string; log?: string }>(`/api/tenant/tunnels/${id}/deploy`, 'POST');
      if (r.ok) notify('Tunnel deployed & active on both servers!');
      else notify('Deploy finished with warnings — check logs.', true);
      if (r.log) setViewerModal({ title: `Deploy Output (Tunnel #${id})`, text: r.log });
      onRefresh();
    } finally {
      setBusyId(0);
    }
  }

  async function tunnelAction(id: number, action: 'start' | 'stop' | 'restart' | 'status' | 'cleanup') {
    setBusyId(id);
    try {
      const r = await api<{ output?: string }>(`/api/tenant/tunnels/${id}/action`, 'POST', { action });
      notify(r.output || r.error || 'Done', !r.ok);
      onRefresh();
    } finally {
      setBusyId(0);
    }
  }

  async function pingTunnel(id: number) {
    setBusyId(id);
    notify('Measuring 2-way ping (IR ⇄ FR)...');
    try {
      const r = await api<{ ping?: string }>(`/api/tenant/tunnels/${id}/ping`, 'POST');
      if (r.ok) {
        notify(r.ping || 'Ping updated');
        onRefresh();
      } else notify(r.error || 'Ping failed', true);
    } finally {
      setBusyId(0);
    }
  }

  async function showMikrotik(t: TunnelItem) {
    const r = await api<{ script?: string }>(`/api/tenant/tunnels/${t.id}/mikrotik`, 'POST');
    if (r.ok && r.script) {
      setViewerModal({ title: `MikroTik RouterOS v7 Script — ${t.name}`, text: r.script });
    } else notify(r.error || 'Failed', true);
  }

  async function handleDeleteTunnel(id: number) {
    if (!confirm('Remove tunnel from panel and clean remote systemd services?')) return;
    await api(`/api/tenant/tunnels/${id}/action`, 'POST', { action: 'cleanup' });
    const r = await api(`/api/tenant/tunnels/${id}`, 'DELETE');
    if (r.ok) {
      notify('Tunnel cleaned & deleted.');
      onRefresh();
    } else notify(r.error || 'Failed', true);
  }

  async function runDiag(kind: 'mtu' | 'mtr') {
    if (!diagSrc || !diagDst.trim()) {
      notify('Select source server and target host.', true);
      return;
    }
    setDiagBusy(true);
    setDiagOut(kind === 'mtu' ? 'Running Path MTU Discovery...' : 'Running MTR / Traceroute...');
    try {
      const r = await api<{ output?: string }>(`/api/tenant/diag/${kind}`, 'POST', {
        source_server_id: diagSrc,
        target_host: diagDst.trim(),
      });
      setDiagOut(r.output || r.error || '(no output)');
    } finally {
      setDiagBusy(false);
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div className="toolbar" style={{ marginBottom: 0 }}>
        <div>
          <h3>Interactive Tunnel Topology &amp; Deployment</h3>
          <div style={{ fontSize: '0.78rem', color: 'var(--muted)' }}>
            Quota: {tunnels.length} / {maxTunnels} • Drag a wire between two servers to create &amp; deploy a real tunnel over SSH!
          </div>
        </div>
      </div>

      {pendingConn && (
        <form className="card" style={{ borderColor: 'var(--accent)' }} onSubmit={handleCreateTunnel}>
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
              <label>Engine &amp; Transport</label>
              <div className="row2">
                <select value={engine} onChange={(e) => setEngine(e.target.value)}>
                  <option value="backhaul">Backhaul</option>
                  <option value="rathole">Rathole</option>
                  <option value="paqet">Paqet</option>
                  <option value="gre">GRE (IPv4 Kernel)</option>
                  <option value="ip6gre">IP6GRE (IPv6 Kernel)</option>
                  <option value="vxlan">VXLAN (L2 Kernel)</option>
                </select>
                <select value={transport} onChange={(e) => setTransport(e.target.value)}>
                  <option value="tcp">TCP</option>
                  <option value="tcpmux">TCPMUX</option>
                  <option value="ws">WebSocket</option>
                  <option value="udp">UDP</option>
                </select>
              </div>
            </div>
          </div>
          <div className="row2">
            <div className="field">
              <label>Tunnel Port / MTU</label>
              <div className="row2">
                <input type="number" value={tunPort} onChange={(e) => setTunPort(Number(e.target.value))} />
                <input type="number" value={mtu} onChange={(e) => setMtu(Number(e.target.value))} />
              </div>
            </div>
            <div className="field">
              <label>Forwarded Ports (comma separated)</label>
              <input className="mono" value={ports} onChange={(e) => setPorts(e.target.value)} />
            </div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 10 }}>
            <label style={{ display: 'inline-flex', alignItems: 'center', gap: 8, fontSize: '0.82rem', cursor: 'pointer' }}>
              <input type="checkbox" style={{ width: 'auto' }} checked={autoDeploy} onChange={(e) => setAutoDeploy(e.target.checked)} />
              Deploy immediately over SSH to both servers
            </label>
            <button type="submit" className="btn btn-primary">
              <Rocket size={15} /> Create {autoDeploy ? '& Deploy' : 'Draft'}
            </button>
          </div>
        </form>
      )}

      <div className="topo-box" style={{ marginBottom: 0 }}>
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

      {/* Active Tunnels List */}
      <div className="card">
        <div className="toolbar">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Network size={17} style={{ color: 'var(--accent)' }} />
            <strong>Managed Tunnels ({tunnels.length})</strong>
          </div>
        </div>
        {tunnels.length === 0 ? (
          <div style={{ fontSize: '0.82rem', color: 'var(--muted)' }}>
            Drag a wire between any two server nodes on the canvas above to create your first tunnel.
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {tunnels.map((t) => (
              <div
                key={t.id}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  padding: '12px 14px',
                  borderRadius: 14,
                  background: 'var(--row)',
                  border: '1px solid var(--border)',
                  gap: 10,
                  flexWrap: 'wrap',
                }}
              >
                <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                    <strong>{t.name}</strong>
                    <span className="badge badge-accent mono">{t.engine.toUpperCase()}</span>
                    <span className={`badge ${t.status === 'active' ? 'badge-green' : t.status === 'error' ? 'badge-red' : ''}`}>
                      {t.status.toUpperCase()}
                    </span>
                    {t.last_ping && <span className="badge badge-green mono">{t.last_ping}</span>}
                  </div>
                  <div className="mono" style={{ fontSize: '0.76rem', color: 'var(--muted)' }}>
                    Port :{t.tunnel_port} • Fwd: {t.ports} • MTU: {t.mtu}
                  </div>
                </div>

                <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                  <button className="btn btn-primary btn-sm" disabled={busyId === t.id} onClick={() => deployTunnel(t.id)}>
                    <Rocket size={13} /> Deploy
                  </button>
                  <button className="btn btn-sm" disabled={busyId === t.id} onClick={() => tunnelAction(t.id, 'restart')}>
                    <RotateCw size={13} /> Restart
                  </button>
                  <button className="btn btn-warn btn-sm" disabled={busyId === t.id} onClick={() => tunnelAction(t.id, 'stop')}>
                    <Square size={13} /> Stop
                  </button>
                  <button className="btn btn-sm" disabled={busyId === t.id} onClick={() => pingTunnel(t.id)}>
                    <Activity size={13} /> 2-Way Ping
                  </button>
                  {(t.engine === 'gre' || t.engine === 'ip6gre') && (
                    <button className="btn btn-sm" onClick={() => showMikrotik(t)}>
                      <FileCode2 size={13} /> MikroTik
                    </button>
                  )}
                  {t.last_log && (
                    <button className="btn btn-sm" onClick={() => setViewerModal({ title: `Logs — ${t.name}`, text: t.last_log })}>
                      <Terminal size={13} /> Logs
                    </button>
                  )}
                  <button className="btn btn-danger btn-sm" disabled={busyId === t.id} onClick={() => handleDeleteTunnel(t.id)}>
                    <Trash2 size={13} />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Network Diagnostics: Path MTU & MTR */}
      <div className="card">
        <div className="toolbar">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Route size={17} style={{ color: 'var(--accent)' }} />
            <strong>Network Diagnostics (Path MTU Discovery &amp; MTR Trace)</strong>
          </div>
        </div>
        <div className="row2" style={{ marginBottom: 10 }}>
          <div className="field" style={{ marginBottom: 0 }}>
            <label>Source Server (SSH)</label>
            <select value={diagSrc} onChange={(e) => setDiagSrc(Number(e.target.value))}>
              {servers.map((s) => (
                <option key={s.id} value={s.id}>{s.name} ({s.host})</option>
              ))}
            </select>
          </div>
          <div className="field" style={{ marginBottom: 0 }}>
            <label>Target Host / IP</label>
            <input className="mono" value={diagDst} onChange={(e) => setDiagDst(e.target.value)} placeholder="1.1.1.1 or Exit Server IP" />
          </div>
        </div>
        <div style={{ display: 'flex', gap: 8, marginBottom: 10, flexWrap: 'wrap' }}>
          <button className="btn btn-primary btn-sm" disabled={diagBusy} onClick={() => runDiag('mtu')}>
            <Gauge size={14} /> Discover Path MTU
          </button>
          <button className="btn btn-sm" disabled={diagBusy} onClick={() => runDiag('mtr')}>
            <Route size={14} /> Run MTR Trace
          </button>
        </div>
        {diagOut && <pre className="term-screen" style={{ height: 220 }}>{diagOut}</pre>}
      </div>

      {viewerModal && (
        <div className="modal-overlay" onMouseDown={(e) => e.target === e.currentTarget && setViewerModal(null)}>
          <div className="ops-modal" style={{ maxWidth: 820 }}>
            <div className="toolbar" style={{ marginBottom: 0 }}>
              <strong style={{ color: 'var(--accent)' }}>{viewerModal.title}</strong>
              <button className="btn btn-danger btn-sm" onClick={() => setViewerModal(null)}>Close</button>
            </div>
            <pre className="term-screen">{viewerModal.text}</pre>
          </div>
        </div>
      )}
    </div>
  );
}
