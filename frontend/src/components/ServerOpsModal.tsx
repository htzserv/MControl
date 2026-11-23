import React, { useEffect, useRef, useState } from 'react';
import {
  Terminal as TermIcon,
  FolderOpen,
  Shield,
  Cog,
  Cpu,
  Maximize2,
  Minimize2,
  Upload,
  Download,
  Trash2,
  ArrowUp,
  Play,
  FolderPlus,
  X,
} from 'lucide-react';
import { api } from '../api';
import type { ServerItem } from '../types';

export type OpsTab = 'terminal' | 'sftp' | 'firewall' | 'services' | 'processes';

interface FileEntry {
  type: 'dir' | 'file';
  size: string;
  name: string;
}

interface SvcItem {
  unit: string;
  active: string;
  sub: string;
}

interface ProcItem {
  pid: number;
  cpu: string;
  mem: string;
  name: string;
}

interface Props {
  server: ServerItem;
  initialTab: OpsTab;
  onClose: () => void;
  notify: (msg: string, err?: boolean) => void;
}

export function ServerOpsModal({ server, initialTab, onClose, notify }: Props) {
  const [tab, setTab] = useState<OpsTab>(initialTab);
  const [full, setFull] = useState(false);

  // Terminal state
  const [termOut, setTermOut] = useState(`Connected to ${server.name} (${server.username}@${server.host}:${server.port})\nReady for single or multi-line bash scripts...\n`);
  const [cmd, setCmd] = useState('');
  const [running, setRunning] = useState(false);
  const termRef = useRef<HTMLPreElement | null>(null);

  // SFTP File Manager state
  const [curPath, setCurPath] = useState('/root');
  const [items, setItems] = useState<FileEntry[]>([]);
  const [loadingFiles, setLoadingFiles] = useState(false);
  const [draggingFile, setDraggingFile] = useState(false);
  const [editPath, setEditPath] = useState('');
  const [editContent, setEditContent] = useState('');
  const uploadInputRef = useRef<HTMLInputElement | null>(null);

  // Firewall state
  const [fwOut, setFwOut] = useState('');
  const [fwPort, setFwPort] = useState(443);
  const [fwProto, setFwProto] = useState<'tcp' | 'udp'>('tcp');

  // Services state
  const [services, setServices] = useState<SvcItem[]>([]);

  // Processes & Security state
  const [procs, setProcs] = useState<ProcItem[]>([]);
  const [secLog, setSecLog] = useState('');
  const [banIP, setBanIP] = useState('');

  useEffect(() => {
    if (tab === 'sftp') loadFiles(curPath);
    if (tab === 'firewall') loadFirewall();
    if (tab === 'services') loadServices();
    if (tab === 'processes') loadProcesses();
  }, [tab]);

  async function runCmd() {
    const clean = cmd.replace(/\r\n/g, '\n').trim();
    if (!clean || running) return;
    const prompt = clean
      .split('\n')
      .map((l, i) => `${i === 0 ? '$ ' : '> '}${l}`)
      .join('\n');
    setTermOut((prev) => `${prev}\n${prompt}\n...`);
    setCmd('');
    setRunning(true);
    try {
      const r = await api<{ output?: string; exit?: string }>(`/api/tenant/servers/${server.id}/exec`, 'POST', {
        command: clean,
      });
      const resTxt = r.ok
        ? `${r.output || '(no output)'}${r.exit ? `\n[exit: ${r.exit}]` : ''}`
        : `ERROR: ${r.error || 'execution failed'}`;
      setTermOut((prev) => prev.replace(/\n\.\.\.$/, `\n${resTxt}`));
      window.setTimeout(() => {
        if (termRef.current) termRef.current.scrollTop = termRef.current.scrollHeight;
      }, 30);
    } finally {
      setRunning(false);
    }
  }

  async function loadFiles(targetPath: string) {
    setLoadingFiles(true);
    try {
      const r = await api<{ path: string; items: FileEntry[] }>(`/api/tenant/servers/${server.id}/files`, 'POST', {
        action: 'list',
        path: targetPath,
      });
      if (r.ok) {
        setCurPath(r.path || targetPath);
        setItems(r.items || []);
      } else {
        notify(r.error || 'Failed to list directory', true);
      }
    } finally {
      setLoadingFiles(false);
    }
  }

  function goUpDir() {
    const clean = curPath.replace(/\/+$/, '') || '/';
    const idx = clean.lastIndexOf('/');
    const parent = idx <= 0 ? '/' : clean.slice(0, idx);
    loadFiles(parent);
  }

  async function openFile(fullPath: string) {
    const r = await api<{ content: string }>(`/api/tenant/servers/${server.id}/files`, 'POST', {
      action: 'read',
      path: fullPath,
    });
    if (r.ok) {
      setEditPath(fullPath);
      setEditContent(r.content || '');
    } else notify(r.error || 'Failed to read file', true);
  }

  async function saveFile() {
    if (!editPath) return;
    const r = await api<{ message?: string }>(`/api/tenant/servers/${server.id}/files`, 'POST', {
      action: 'write',
      path: editPath,
      content: editContent,
    });
    notify(r.message || r.error || 'Saved', !r.ok);
  }

  async function uploadBrowserFile(file: File) {
    const reader = new FileReader();
    reader.onload = async () => {
      const b64 = String(reader.result || '').split(',')[1] || '';
      const dir = curPath.replace(/\/+$/, '');
      const dest = `${dir}/${file.name}`;
      notify(`Uploading ${file.name}...`);
      const r = await api<{ message?: string }>(`/api/tenant/servers/${server.id}/files`, 'POST', {
        action: 'upload',
        path: dest,
        content: b64,
      });
      notify(r.message || r.error || 'Uploaded', !r.ok);
      loadFiles(curPath);
    };
    reader.readAsDataURL(file);
  }

  async function downloadFile(fullPath: string) {
    notify(`Downloading ${fullPath}...`);
    const r = await api<{ b64: string; filename: string }>(`/api/tenant/servers/${server.id}/files`, 'POST', {
      action: 'download',
      path: fullPath,
    });
    if (!r.ok || !r.b64) {
      notify(r.error || 'Download failed', true);
      return;
    }
    const bin = atob(r.b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    const url = URL.createObjectURL(new Blob([bytes]));
    const a = document.createElement('a');
    a.href = url;
    a.download = r.filename || 'file.bin';
    a.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1500);
  }

  async function deleteFile(fullPath: string) {
    if (!confirm(`Delete ${fullPath}?`)) return;
    const r = await api(`/api/tenant/servers/${server.id}/files`, 'POST', { action: 'delete', path: fullPath });
    if (r.ok) {
      notify('Deleted.');
      if (editPath === fullPath) setEditPath('');
      loadFiles(curPath);
    } else notify(r.error || 'Delete failed', true);
  }

  async function createFolder() {
    const folder = prompt('New folder name:');
    if (!folder) return;
    const dir = curPath.replace(/\/+$/, '');
    const r = await api(`/api/tenant/servers/${server.id}/files`, 'POST', {
      action: 'mkdir',
      path: `${dir}/${folder.trim()}`,
    });
    if (r.ok) loadFiles(curPath);
  }

  async function loadFirewall() {
    const r = await api<{ output: string }>(`/api/tenant/servers/${server.id}/firewall`);
    if (r.ok) setFwOut(r.output || '');
  }

  async function fwAction(action: 'allow' | 'deny') {
    const r = await api<{ output: string }>(`/api/tenant/servers/${server.id}/firewall`, 'POST', {
      action,
      port: fwPort,
      proto: fwProto,
    });
    notify(r.output || r.error || 'Done', !r.ok);
    loadFirewall();
  }

  async function loadServices() {
    const r = await api<{ services: SvcItem[] }>(`/api/tenant/servers/${server.id}/services`);
    if (r.ok) setServices(r.services || []);
  }

  async function svcAction(unit: string, action: 'start' | 'stop' | 'restart') {
    const r = await api(`/api/tenant/servers/${server.id}/services`, 'POST', { unit, action });
    notify(r.ok ? `${action.toUpperCase()} ${unit}` : r.error || 'Failed', !r.ok);
    loadServices();
  }

  async function loadProcesses() {
    const r = await api<{ processes: ProcItem[]; security: string }>(`/api/tenant/servers/${server.id}/processes`);
    if (r.ok) {
      setProcs(r.processes || []);
      setSecLog(r.security || 'No failed SSH attempts found.');
    }
  }

  async function killProc(pid: number, name: string) {
    if (!confirm(`Kill process ${name} (PID ${pid})?`)) return;
    const r = await api<{ message?: string }>(`/api/tenant/servers/${server.id}/processes`, 'POST', {
      action: 'kill',
      pid,
    });
    notify(r.message || r.error || 'Done', !r.ok);
    loadProcesses();
  }

  async function banAttacker() {
    if (!banIP.trim()) return;
    const r = await api<{ message?: string }>(`/api/tenant/servers/${server.id}/processes`, 'POST', {
      action: 'ban',
      ip: banIP.trim(),
    });
    notify(r.message || r.error || 'Done', !r.ok);
    if (r.ok) setBanIP('');
  }

  return (
    <div className="modal-overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`ops-modal ${full ? 'fullscreen' : ''}`}>
        <div className="toolbar" style={{ marginBottom: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <strong style={{ color: 'var(--accent)', fontSize: '1.02rem' }}>{server.name}</strong>
            <span className="badge mono">{server.host}</span>
          </div>
          <div style={{ display: 'flex', gap: 6 }}>
            <button className="btn btn-sm" onClick={() => setFull(!full)}>
              {full ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
              {full ? 'Restore' : 'Expand'}
            </button>
            <button className="btn btn-danger btn-sm" onClick={onClose}>
              <X size={14} /> Close
            </button>
          </div>
        </div>

        <div className="nav-pill" style={{ alignSelf: 'center' }}>
          <button className={`nav-btn ${tab === 'terminal' ? 'active' : ''}`} onClick={() => setTab('terminal')}>
            <TermIcon size={14} /> Terminal
          </button>
          <button className={`nav-btn ${tab === 'sftp' ? 'active' : ''}`} onClick={() => setTab('sftp')}>
            <FolderOpen size={14} /> SFTP / Files
          </button>
          <button className={`nav-btn ${tab === 'firewall' ? 'active' : ''}`} onClick={() => setTab('firewall')}>
            <Shield size={14} /> Ports / FW
          </button>
          <button className={`nav-btn ${tab === 'services' ? 'active' : ''}`} onClick={() => setTab('services')}>
            <Cog size={14} /> Services
          </button>
          <button className={`nav-btn ${tab === 'processes' ? 'active' : ''}`} onClick={() => setTab('processes')}>
            <Cpu size={14} /> Procs / Sec
          </button>
        </div>

        {tab === 'terminal' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <pre ref={termRef} className="term-screen">{termOut}</pre>
            <textarea
              className="code-box"
              rows={3}
              value={cmd}
              onChange={(e) => setCmd(e.target.value)}
              placeholder={'Enter single or multi-line bash scripts...\nEnter = new line  |  Ctrl+Enter = Run'}
              onKeyDown={(e) => {
                if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                  e.preventDefault();
                  runCmd();
                }
              }}
            />
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
              <span style={{ fontSize: '0.75rem', color: 'var(--muted)' }}>
                Multi-line supported • Press <strong>Ctrl+Enter</strong> or click Run
              </span>
              <div style={{ display: 'flex', gap: 6 }}>
                <button className="btn btn-sm" onClick={() => setTermOut('')}>Clear</button>
                <button className="btn btn-primary btn-sm" disabled={running} onClick={runCmd}>
                  <Play size={14} /> {running ? 'Running...' : 'Run Script'}
                </button>
              </div>
            </div>
          </div>
        )}

        {tab === 'sftp' && (
          <div
            className={`sftp-dropzone ${draggingFile ? 'dragging' : ''}`}
            onDragOver={(e) => {
              e.preventDefault();
              setDraggingFile(true);
            }}
            onDragLeave={() => setDraggingFile(false)}
            onDrop={(e) => {
              e.preventDefault();
              setDraggingFile(false);
              if (e.dataTransfer.files && e.dataTransfer.files[0]) {
                uploadBrowserFile(e.dataTransfer.files[0]);
              }
            }}
          >
            <div style={{ display: 'flex', gap: 6, marginBottom: 10, flexWrap: 'wrap' }}>
              <button className="btn btn-sm" onClick={goUpDir}><ArrowUp size={14} /> Up</button>
              <input
                className="mono"
                style={{ flex: 1, minWidth: 180 }}
                value={curPath}
                onChange={(e) => setCurPath(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && loadFiles(curPath)}
              />
              <button className="btn btn-primary btn-sm" onClick={() => loadFiles(curPath)}>Open</button>
              <button className="btn btn-sm" onClick={createFolder}><FolderPlus size={14} /></button>
              <input
                ref={uploadInputRef}
                type="file"
                style={{ display: 'none' }}
                onChange={(e) => e.target.files?.[0] && uploadBrowserFile(e.target.files[0])}
              />
              <button className="btn btn-warn btn-sm" onClick={() => uploadInputRef.current?.click()}>
                <Upload size={14} /> Upload / Drop File
              </button>
            </div>

            <div className={`sftp-split ${editPath ? 'editing' : ''}`}>
              <div style={{ maxHeight: 420, overflow: 'auto', display: 'flex', flexDirection: 'column', gap: 5 }}>
                {loadingFiles ? (
                  <div style={{ color: 'var(--muted)', fontSize: '0.82rem' }}>Loading directory...</div>
                ) : items.length === 0 ? (
                  <div style={{ color: 'var(--muted)', fontSize: '0.82rem' }}>Empty folder — drag &amp; drop any file here to upload.</div>
                ) : (
                  items.map((it) => {
                    const fullPath = `${curPath.replace(/\/+$/, '')}/${it.name}`;
                    return (
                      <div
                        key={it.name}
                        className="file-row"
                        onClick={() => (it.type === 'dir' ? loadFiles(fullPath) : openFile(fullPath))}
                      >
                        <span>
                          {it.type === 'dir' ? '📁 ' : '📄 '}
                          <strong>{it.name}</strong>
                        </span>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                          <span className="mono" style={{ fontSize: '0.72rem', color: 'var(--muted)' }}>
                            {it.type === 'dir' ? 'DIR' : `${it.size} B`}
                          </span>
                          {it.type === 'file' && (
                            <button
                              className="btn btn-sm"
                              style={{ padding: '3px 7px' }}
                              onClick={(e) => {
                                e.stopPropagation();
                                downloadFile(fullPath);
                              }}
                              title="Download"
                            >
                              <Download size={13} />
                            </button>
                          )}
                          <button
                            className="btn btn-danger btn-sm"
                            style={{ padding: '3px 7px' }}
                            onClick={(e) => {
                              e.stopPropagation();
                              deleteFile(fullPath);
                            }}
                            title="Delete"
                          >
                            <Trash2 size={13} />
                          </button>
                        </div>
                      </div>
                    );
                  })
                )}
              </div>

              {editPath && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <div className="toolbar" style={{ marginBottom: 0 }}>
                    <span className="mono" style={{ fontSize: '0.78rem', color: 'var(--accent)', wordBreak: 'break-all' }}>
                      {editPath}
                    </span>
                    <div style={{ display: 'flex', gap: 6 }}>
                      <button className="btn btn-primary btn-sm" onClick={saveFile}>Save File</button>
                      <button className="btn btn-sm" onClick={() => setEditPath('')}>Close</button>
                    </div>
                  </div>
                  <textarea
                    className="code-box"
                    rows={15}
                    value={editContent}
                    onChange={(e) => setEditContent(e.target.value)}
                  />
                </div>
              )}
            </div>
          </div>
        )}

        {tab === 'firewall' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              <input type="number" style={{ width: 120 }} value={fwPort} onChange={(e) => setFwPort(Number(e.target.value))} />
              <select style={{ width: 100 }} value={fwProto} onChange={(e) => setFwProto(e.target.value as 'tcp' | 'udp')}>
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
              <button className="btn btn-primary btn-sm" onClick={() => fwAction('allow')}>Allow Port</button>
              <button className="btn btn-danger btn-sm" onClick={() => fwAction('deny')}>Block Port</button>
            </div>
            <pre className="term-screen">{fwOut || 'Loading listening sockets...'}</pre>
          </div>
        )}

        {tab === 'services' && (
          <div style={{ maxHeight: 420, overflow: 'auto', display: 'flex', flexDirection: 'column', gap: 6 }}>
            {services.map((s) => (
              <div key={s.unit} className="file-row" style={{ cursor: 'default' }}>
                <div>
                  <strong className="mono">{s.unit}</strong>{' '}
                  <span className="badge badge-green">{s.sub}</span>
                </div>
                <div style={{ display: 'flex', gap: 5 }}>
                  <button className="btn btn-primary btn-sm" onClick={() => svcAction(s.unit, 'restart')}>Restart</button>
                  <button className="btn btn-danger btn-sm" onClick={() => svcAction(s.unit, 'stop')}>Stop</button>
                </div>
              </div>
            ))}
          </div>
        )}

        {tab === 'processes' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div style={{ maxHeight: 220, overflow: 'auto', display: 'flex', flexDirection: 'column', gap: 5 }}>
              {procs.map((p) => (
                <div key={p.pid} className="file-row" style={{ cursor: 'default' }}>
                  <div>
                    <span className="badge mono">PID {p.pid}</span> <strong>{p.name}</strong>{' '}
                    <span className="mono" style={{ fontSize: '0.75rem', color: 'var(--muted)' }}>
                      CPU {p.cpu} · RAM {p.mem}
                    </span>
                  </div>
                  <button className="btn btn-danger btn-sm" onClick={() => killProc(p.pid, p.name)}>Kill</button>
                </div>
              ))}
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="mono" value={banIP} onChange={(e) => setBanIP(e.target.value)} placeholder="Ban attacker IP (e.g. 1.2.3.4)" />
              <button className="btn btn-danger btn-sm" onClick={banAttacker}>Ban IP</button>
            </div>
            <pre className="term-screen" style={{ height: 150 }}>{secLog}</pre>
          </div>
        )}
      </div>
    </div>
  );
}
