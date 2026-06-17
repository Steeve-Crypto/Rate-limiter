import React, { useState, useEffect, useMemo } from 'react';
import { 
  Gauge, Activity, Zap, Users, Database, BarChart3, Play, History, 
  Settings, RefreshCw, Download, Trash2, X, Server, Layers
} from 'lucide-react';
import { 
  LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, 
  PieChart, Pie, Cell, BarChart, Bar 
} from 'recharts';
import { Toaster, toast } from 'sonner';

interface CheckResult {
  allowed: boolean;
  remaining: number;
  limit: number;
  retry_after_ms?: number;
  reset_at?: number;
  algorithm: string;
}

interface LogEntry {
  id: number;
  timestamp: string;
  type: string;
  message: string;
  data?: any;
}

interface HealthInfo {
  ok: boolean;
  backend: string;
  node: string;
  cluster_nodes?: number;
}

const API_BASE = '';

const TABS = [
  { id: 'overview', label: 'Overview', icon: BarChart3 },
  { id: 'check', label: 'Rate Check', icon: Zap },
  { id: 'visualize', label: 'Visualizer', icon: Activity },
  { id: 'simulate', label: 'Simulator', icon: Play },
  { id: 'policies', label: 'Policies', icon: Settings },
  { id: 'replication', label: 'Replication', icon: Database },
  { id: 'cluster', label: 'Cluster', icon: Users },
  { id: 'replay', label: 'Replay', icon: History },
  { id: 'admin', label: 'Admin', icon: Layers },
  { id: 'log', label: 'Full Log', icon: History },
] as const;

type TabId = typeof TABS[number]['id'];

const App: React.FC = () => {
  const [activeTab, setActiveTab] = useState<TabId>('overview');
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [health, setHealth] = useState<HealthInfo | null>(null);
  const [logFilter, setLogFilter] = useState('');
  const [showLogPanel, setShowLogPanel] = useState(true);

  // Form states
  const [checkForm, setCheckForm] = useState({
    key: 'user:42:api',
    max_tokens: 100,
    window_seconds: 60,
    algorithm: 'token_bucket' as 'token_bucket' | 'sliding_window' | 'leaky_bucket',
    cost: 1,
    labels: '{"tier":"pro"}'
  });
  const [lastCheck, setLastCheck] = useState<CheckResult | null>(null);

  const [vizKey, setVizKey] = useState('user:42:api');
  const [vizData, setVizData] = useState<any>(null);
  const [isLive, setIsLive] = useState(false);
  const [eventSource, setEventSource] = useState<EventSource | null>(null);

  const [simForm, setSimForm] = useState({
    key: 'user:42:api',
    max_tokens: 100,
    window_seconds: 60,
    algorithm: 'token_bucket',
    costs: '1,1,1,5,2'
  });
  const [simResults, setSimResults] = useState<any>(null);

  const [policies, setPolicies] = useState<any[]>([]);
  const [newPolicy, setNewPolicy] = useState({
    name: 'api:pro',
    pattern: 'api:*',
    labels: '{}',
    config: { algorithm: 'token_bucket', max_tokens: 200, window_seconds: 60 },
    priority: 100
  });

  const [replicationEvent, setReplicationEvent] = useState({ 
    op: 'upsert', key: 'feature:dark', value: '{"enabled":true}' 
  });
  const [replicatedState, setReplicatedState] = useState<any>(null);

  const [clusterData, setClusterData] = useState<any>(null);
  const [replayReq, setReplayReq] = useState({ from_ts: Date.now() - 3600000, to_ts: Date.now(), key: '' });
  const [replayResults, setReplayResults] = useState<any>(null);
  const [adminKey, setAdminKey] = useState('user:42:api');
  const [adminInspect, setAdminInspect] = useState<any>(null);

  // Persistent Log (localStorage hydrate)
  useEffect(() => {
    const saved = localStorage.getItem('rateflow_logs');
    if (saved) {
      try { setLogs(JSON.parse(saved)); } catch {}
    }
  }, []);

  useEffect(() => {
    localStorage.setItem('rateflow_logs', JSON.stringify(logs.slice(0, 100)));
  }, [logs]);

  const addLog = (type: string, message: string, data?: any) => {
    const entry: LogEntry = {
      id: Date.now() + Math.random(),
      timestamp: new Date().toLocaleTimeString(),
      type,
      message,
      data
    };
    setLogs(prev => [entry, ...prev].slice(0, 80));
  };

  const clearLog = () => {
    setLogs([]);
    localStorage.removeItem('rateflow_logs');
    toast.info('Log cleared');
  };

  const filteredLogs = useMemo(() => {
    if (!logFilter) return logs;
    const q = logFilter.toLowerCase();
    return logs.filter(l =>
      l.type.toLowerCase().includes(q) ||
      l.message.toLowerCase().includes(q) ||
      JSON.stringify(l.data || '').toLowerCase().includes(q)
    );
  }, [logs, logFilter]);

  // Export functions
  const exportLog = (format: 'json' | 'csv') => {
    if (logs.length === 0) {
      toast.error('No log entries to export');
      return;
    }
    if (format === 'json') {
      const blob = new Blob([JSON.stringify(logs, null, 2)], { type: 'application/json' });
      downloadBlob(blob, `rateflow-log-${Date.now()}.json`);
    } else {
      const headers = ['timestamp', 'type', 'message', 'data'];
      const rows = logs.map(l => [
        l.timestamp,
        l.type,
        `"${l.message.replace(/"/g, '""')}"`,
        l.data ? `"${JSON.stringify(l.data).replace(/"/g, '""')}"` : ''
      ]);
      const csv = [headers.join(','), ...rows.map(r => r.join(','))].join('\n');
      const blob = new Blob([csv], { type: 'text/csv' });
      downloadBlob(blob, `rateflow-log-${Date.now()}.csv`);
    }
    addLog('export', `Exported ${logs.length} entries as ${format.toUpperCase()}`);
    toast.success(`Exported ${format.toUpperCase()}`);
  };

  const downloadBlob = (blob: Blob, filename: string) => {
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  // Generic API call
  const apiCall = async (url: string, options: RequestInit = {}, logType = 'api') => {
    setIsLoading(true);
    try {
      const res = await fetch(`${API_BASE}${url}`, {
        headers: { 'Content-Type': 'application/json', ...options.headers },
        ...options
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
      addLog(logType, `${options.method || 'GET'} ${url}`, data);
      return data;
    } catch (err: any) {
      const msg = err.message || 'Request failed';
      addLog('error', msg);
      toast.error(msg);
      throw err;
    } finally {
      setIsLoading(false);
    }
  };

  // Fetch health + cluster periodically
  const fetchHealth = async () => {
    try {
      const data = await fetch(`${API_BASE}/health`).then(r => r.json());
      setHealth(data);
    } catch {
      setHealth({ ok: false, backend: 'offline', node: 'unknown' });
    }
  };

  const fetchCluster = async () => {
    try {
      const data = await apiCall('/v1/cluster/nodes', {}, 'cluster');
      setClusterData(data);
    } catch {}
  };

  useEffect(() => {
    fetchHealth();
    loadPolicies();
    loadReplicated();
    fetchCluster();
    const poll = setInterval(() => {
      fetchHealth();
      if (activeTab === 'cluster' || activeTab === 'overview') fetchCluster();
    }, 15000);
    return () => clearInterval(poll);
  }, [activeTab]);

  // CHECK
  const handleCheck = async () => {
    try {
      const labels = JSON.parse(checkForm.labels || '{}');
      const data: CheckResult = await apiCall('/v1/check', {
        method: 'POST',
        body: JSON.stringify({ ...checkForm, labels })
      }, 'check');
      setLastCheck(data);
      addLog('check', `${data.allowed ? 'Allowed' : 'Limited'} for ${checkForm.key}`, { 
        key: checkForm.key, allowed: data.allowed, remaining: data.remaining, limit: data.limit 
      });
      toast.success(data.allowed ? `Allowed • ${data.remaining} left` : 'Rate limited');
    } catch {}
  };

  // VISUALIZE + SSE
  const loadVisualize = async () => {
    try {
      const data = await apiCall(`/v1/visualize?key=${encodeURIComponent(vizKey)}&include_history=true`, {}, 'visualize');
      setVizData(data);
    } catch {}
  };

  const startLiveViz = () => {
    if (eventSource) eventSource.close();
    const es = new EventSource(`${API_BASE}/v1/visualize/stream?key=${encodeURIComponent(vizKey)}`);
    setEventSource(es);
    setIsLive(true);
    es.onmessage = (ev) => {
      try {
        const d = JSON.parse(ev.data);
        setVizData({ current: d });
        addLog('live', `Live viz ${vizKey}`);
      } catch {}
    };
    es.onerror = () => {
      toast.error('SSE error');
      stopLiveViz();
    };
    addLog('live', `Started live stream for ${vizKey}`);
  };

  const stopLiveViz = () => {
    if (eventSource) { eventSource.close(); setEventSource(null); }
    setIsLive(false);
  };

  // SIMULATE
  const handleSimulate = async () => {
    try {
      const costs = simForm.costs.split(',').map(c => parseInt(c.trim(), 10));
      const data = await apiCall('/v1/simulate', {
        method: 'POST',
        body: JSON.stringify({ ...simForm, costs })
      }, 'simulate');
      setSimResults(data);
      addLog('simulate', `Simulated ${costs.length} costs for ${simForm.key}`, data);
    } catch {}
  };

  // POLICIES
  const loadPolicies = async () => {
    try {
      const data = await apiCall('/v1/policies', {}, 'policy');
      setPolicies(Array.isArray(data) ? data : []);
    } catch {}
  };

  const addPolicy = async () => {
    try {
      const labels = JSON.parse(newPolicy.labels || '{}');
      await apiCall('/v1/policies', {
        method: 'POST',
        body: JSON.stringify({ ...newPolicy, labels, config: newPolicy.config })
      }, 'policy');
      toast.success('Policy added');
      loadPolicies();
    } catch {}
  };

  // REPLICATION
  const emitReplication = async () => {
    try {
      let value: any = replicationEvent.value;
      try { value = JSON.parse(replicationEvent.value); } catch { value = replicationEvent.value; }
      await apiCall('/v1/replicate', {
        method: 'POST',
        body: JSON.stringify({ op: replicationEvent.op, key: replicationEvent.key, value })
      }, 'replicate');
      toast.success('Replication event sent');
      loadReplicated();
    } catch {}
  };

  const loadReplicated = async () => {
    try {
      const data = await apiCall(`/v1/replicated/${encodeURIComponent(replicationEvent.key)}`, {}, 'replicate');
      setReplicatedState(data);
    } catch {}
  };

  // REPLAY
  const runReplay = async () => {
    try {
      const data = await apiCall('/v1/replay', {
        method: 'POST',
        body: JSON.stringify(replayReq)
      }, 'replay');
      setReplayResults(data);
    } catch {}
  };

  // ADMIN
  const doInspect = async () => {
    try {
      const data = await apiCall(`/v1/admin/inspect?key=${encodeURIComponent(adminKey)}`, {}, 'admin');
      setAdminInspect(data);
    } catch {}
  };
  const doReset = async () => {
    try {
      await apiCall(`/v1/admin/reset?key=${encodeURIComponent(adminKey)}`, { method: 'POST' }, 'admin');
      toast.success('Reset');
    } catch {}
  };

  // Cluster load
  const loadClusterViz = async () => {
    try {
      const data = await apiCall('/v1/cluster/visualize?key=cluster-demo', {}, 'cluster');
      setClusterData(data);
    } catch {}
  };

  // CHART DATA
  const pieData = useMemo(() => {
    const allowed = logs.filter(l => l.data?.allowed === true).length;
    const limited = logs.filter(l => l.data && l.data.allowed === false).length;
    return [
      { name: 'Allowed', value: allowed || 0 },
      { name: 'Limited', value: limited || 0 },
    ];
  }, [logs]);

  const lineData = useMemo(() => {
    return logs.slice(0, 15).reverse().map((l, i) => ({
      idx: i,
      allowed: l.data?.allowed === true ? 1 : 0,
      limited: l.data?.allowed === false ? 1 : 0,
    }));
  }, [logs]);

  const activityData = useMemo(() => {
    const byType: Record<string, number> = {};
    logs.forEach(l => { byType[l.type] = (byType[l.type] || 0) + 1; });
    return Object.entries(byType).map(([name, value]) => ({ name, value }));
  }, [logs]);

  const COLORS = ['#22c55e', '#ef4444'];
  const cardClass = "bg-zinc-900 border border-zinc-800 rounded-2xl p-6";
  const buttonClass = "px-4 py-2 bg-indigo-600 hover:bg-indigo-700 disabled:opacity-60 text-white rounded-xl font-medium flex items-center gap-2 transition";
  const inputClass = "w-full bg-zinc-950 border border-zinc-800 rounded-xl px-3 py-2 text-sm outline-none focus:border-indigo-500";

  // Log entry renderer (shared)
  const renderLogList = () => (
    <div className="flex-1 overflow-auto p-3 space-y-1.5 text-xs font-mono">
      {filteredLogs.length === 0 && <div className="text-zinc-500 px-2 py-1">No matching results.</div>}
      {filteredLogs.map(entry => {
        const isAllowed = entry.data?.allowed === true;
        const isLimited = entry.data?.allowed === false;
        return (
          <div key={entry.id} className="flex gap-2 px-2 py-1.5 rounded-lg bg-zinc-950/60 border border-zinc-900">
            <span className="text-zinc-500 w-16 shrink-0">{entry.timestamp}</span>
            <span className={`shrink-0 px-1.5 rounded text-[10px] self-start mt-px
              ${entry.type === 'error' ? 'bg-rose-500/20 text-rose-400' : 
                entry.type === 'check' ? (isAllowed ? 'bg-emerald-500/20 text-emerald-400' : isLimited ? 'bg-rose-500/20 text-rose-400' : 'bg-zinc-700') : 'bg-zinc-700 text-zinc-400'}`}>
              {entry.type}
            </span>
            <span className="flex-1 text-zinc-200">{entry.message}</span>
            {entry.data && (
              <span className="text-[10px] text-zinc-500 truncate max-w-[120px]">{JSON.stringify(entry.data).slice(0,70)}</span>
            )}
          </div>
        );
      })}
    </div>
  );

  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-200 flex flex-col">
      <Toaster position="top-right" richColors closeButton />

      {/* Header */}
      <header className="h-14 border-b border-zinc-800 bg-zinc-900/90 backdrop-blur sticky top-0 z-50 flex items-center px-6">
        <div className="flex items-center gap-3 flex-1">
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-2xl bg-indigo-600 flex items-center justify-center"><Gauge className="w-4 h-4" /></div>
            <div>
              <div className="font-semibold tracking-[-0.5px] text-lg leading-none">RateFlow</div>
              <div className="text-[9px] text-zinc-500 -mt-0.5">CONTROL PLANE</div>
            </div>
          </div>
          <div className="ml-4 text-xs px-2.5 py-px bg-zinc-900 border border-zinc-800 rounded-full flex items-center gap-1">
            <Server className="w-3 h-3" /> {health?.backend || '—'} 
            <span className="text-emerald-400">• {health?.node || 'node'}</span>
          </div>
          {health && <div className={`text-xs px-2 py-px rounded ${health.ok ? 'text-emerald-400' : 'text-rose-400'}`}>{health.ok ? 'HEALTHY' : 'DEGRADED'}</div>}
        </div>

        <div className="flex items-center gap-2 text-sm">
          <button onClick={() => { fetchHealth(); fetchCluster(); }} className="flex items-center gap-1.5 text-xs px-3 py-1.5 hover:bg-zinc-900 rounded-xl border border-zinc-800">
            <RefreshCw className="w-3.5 h-3.5" /> Sync
          </button>
          <button onClick={() => setShowLogPanel(!showLogPanel)} className="text-xs px-3 py-1.5 bg-zinc-900 border border-zinc-800 rounded-xl flex gap-1 items-center">
            <History className="w-3.5 h-3.5" /> {showLogPanel ? 'Hide' : 'Show'} Log
          </button>
          <div className="text-[10px] text-zinc-500 px-2">React + Recharts</div>
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {/* Sidebar Nav */}
        <div className="w-60 bg-zinc-900 border-r border-zinc-800 p-3 flex-shrink-0 overflow-auto">
          <div className="px-3 text-[10px] tracking-[1px] text-zinc-500 mb-1.5 mt-1">NAVIGATION</div>
          {TABS.map(tab => {
            const Icon = tab.icon;
            const active = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                onClick={() => setActiveTab(tab.id)}
                className={`w-full flex items-center gap-2.5 px-3 py-2 mb-0.5 rounded-2xl text-sm font-medium transition ${active ? 'bg-indigo-600 text-white shadow' : 'hover:bg-zinc-800 text-zinc-400'}`}
              >
                <Icon className="w-4 h-4" /> {tab.label}
              </button>
            );
          })}
          <div className="mt-6 px-3 text-[10px] text-zinc-500">QUICK</div>
          <button onClick={() => { setActiveTab('check'); }} className="mt-1 w-full text-left text-xs px-3 py-2 hover:bg-zinc-800 rounded-xl flex gap-2 items-center"><Zap className="w-3.5 h-3.5"/> Quick Check</button>
        </div>

        {/* Main Content Area */}
        <div className="flex-1 overflow-auto p-8 pb-12" style={{maxWidth: showLogPanel ? 'calc(100% - 320px)' : '100%'}}>
          <div className="max-w-5xl">
            <div className="mb-8">
              <h1 className="text-3xl font-semibold tracking-tight">Control Center</h1>
              <p className="text-zinc-400">Real-time rate limiting, simulation, policies &amp; cluster replication.</p>
            </div>

            {/* OVERVIEW */}
            {activeTab === 'overview' && (
              <div className="space-y-6">
                <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
                  <div className="bg-zinc-900 border border-zinc-800 rounded-3xl p-6">
                    <div className="uppercase text-xs tracking-widest text-zinc-500">Backend</div>
                    <div className="mt-1 text-3xl font-semibold">{health?.backend || 'inmemory'}</div>
                    <div className="text-emerald-400 text-xs mt-1">Node {health?.node}</div>
                  </div>
                  <div className="bg-zinc-900 border border-zinc-800 rounded-3xl p-6">
                    <div className="uppercase text-xs tracking-widest text-zinc-500">Cluster Nodes</div>
                    <div className="mt-1 text-3xl font-semibold tabular-nums">{health?.cluster_nodes ?? clusterData?.nodes?.length ?? 1}</div>
                    <div className="text-xs text-zinc-400 mt-1">Live from registry</div>
                  </div>
                  <div className="bg-zinc-900 border border-zinc-800 rounded-3xl p-6">
                    <div className="uppercase text-xs tracking-widest text-zinc-500">Recent Actions</div>
                    <div className="mt-1 text-3xl font-semibold tabular-nums">{logs.length}</div>
                    <div className="text-xs mt-1 text-emerald-400">Logged in session</div>
                  </div>
                </div>

                <div className="grid grid-cols-1 lg:grid-cols-5 gap-4">
                  <div className={cardClass + ' lg:col-span-2'}>
                    <div className="font-medium mb-3 flex items-center gap-2"><BarChart3 className="w-4 h-4" /> Distribution</div>
                    <div className="h-56">
                      <ResponsiveContainer width="100%" height="100%">
                        <PieChart>
                          <Pie dataKey="value" data={pieData} cx="50%" cy="48%" innerRadius={68} outerRadius={100}>
                            {pieData.map((_, idx) => <Cell key={idx} fill={COLORS[idx % COLORS.length]} />)}
                          </Pie>
                          <Tooltip />
                        </PieChart>
                      </ResponsiveContainer>
                    </div>
                    <div className="flex gap-3 text-xs pt-1">
                      <div><span className="text-emerald-400">■</span> Allowed</div>
                      <div><span className="text-rose-400">■</span> Limited</div>
                    </div>
                  </div>

                  <div className={cardClass + ' lg:col-span-3'}>
                    <div className="font-medium mb-3">Activity Trend</div>
                    <div className="h-56">
                      <ResponsiveContainer>
                        <LineChart data={lineData.length ? lineData : [{idx:0,allowed:1,limited:0}]}>
                          <CartesianGrid strokeDasharray="3 3" stroke="#27272a" />
                          <XAxis dataKey="idx" />
                          <YAxis />
                          <Tooltip />
                          <Line type="step" dataKey="allowed" stroke="#22c55e" dot={false} strokeWidth={2} />
                          <Line type="step" dataKey="limited" stroke="#ef4444" dot={false} strokeWidth={2} />
                        </LineChart>
                      </ResponsiveContainer>
                    </div>
                  </div>
                </div>

                <div className={cardClass}>
                  <div className="font-medium mb-4">Jump into the tools</div>
                  <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                    {TABS.filter(t => !['overview','log'].includes(t.id)).map(t => (
                      <button key={t.id} onClick={() => setActiveTab(t.id)} className="flex gap-2 items-center px-4 py-3 bg-zinc-950 hover:bg-zinc-900 border border-zinc-800 rounded-2xl text-sm">{t.label}</button>
                    ))}
                  </div>
                </div>
              </div>
            )}

            {/* CHECK TAB */}
            {activeTab === 'check' && (
              <div className={cardClass}>
                <h3 className="font-semibold text-xl mb-6">Rate Limit Check</h3>
                <div className="grid md:grid-cols-2 gap-x-10 gap-y-6">
                  <div className="space-y-4">
                    <div><label className="text-xs text-zinc-400">KEY</label><input value={checkForm.key} onChange={e=>setCheckForm({...checkForm,key:e.target.value})} className={inputClass}/></div>
                    <div className="grid grid-cols-2 gap-3">
                      <div><label className="text-xs text-zinc-400">ALGORITHM</label>
                        <select value={checkForm.algorithm} onChange={e=>setCheckForm({...checkForm,algorithm:e.target.value as any})} className={inputClass}>
                          <option value="token_bucket">token_bucket</option><option value="sliding_window">sliding_window</option><option value="leaky_bucket">leaky_bucket</option>
                        </select>
                      </div>
                      <div><label className="text-xs text-zinc-400">COST</label><input type="number" value={checkForm.cost} onChange={e=>setCheckForm({...checkForm,cost:parseInt(e.target.value)||1})} className={inputClass}/></div>
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                      <div><label className="text-xs text-zinc-400">MAX TOKENS</label><input type="number" value={checkForm.max_tokens} onChange={e=>setCheckForm({...checkForm,max_tokens:parseInt(e.target.value)||100})} className={inputClass}/></div>
                      <div><label className="text-xs text-zinc-400">WINDOW SECS</label><input type="number" value={checkForm.window_seconds} onChange={e=>setCheckForm({...checkForm,window_seconds:parseInt(e.target.value)||60})} className={inputClass}/></div>
                    </div>
                    <div><label className="text-xs text-zinc-400">LABELS (JSON)</label><input value={checkForm.labels} onChange={e=>setCheckForm({...checkForm,labels:e.target.value})} className={inputClass}/></div>
                    <button onClick={handleCheck} disabled={isLoading} className={buttonClass + ' w-full justify-center'}><Zap className="w-4 h-4"/> EXECUTE CHECK</button>
                  </div>

                  <div>
                    {lastCheck ? (
                      <div className="rounded-2xl border border-zinc-800 bg-black/40 p-6">
                        <div className={`inline-block px-3 py-1 rounded-full text-xs font-medium mb-2 ${lastCheck.allowed ? 'bg-emerald-500/10 text-emerald-400' : 'bg-rose-500/10 text-rose-400'}`}>
                          {lastCheck.allowed ? 'ALLOWED' : 'RATE LIMITED'}
                        </div>
                        <div className="text-5xl font-semibold tabular-nums tracking-tighter mb-4">{lastCheck.remaining}<span className="text-2xl text-zinc-500">/{lastCheck.limit}</span></div>
                        <div className="grid grid-cols-2 text-sm gap-y-1 text-zinc-400">
                          <div>Algorithm</div><div className="font-mono text-right text-zinc-200">{lastCheck.algorithm}</div>
                          <div>Reset</div><div className="font-mono text-right text-zinc-200">{lastCheck.reset_at || '—'}</div>
                        </div>
                      </div>
                    ) : <div className="text-sm text-zinc-500 p-8 border border-dashed border-zinc-800 rounded-2xl">Results will appear here after a check.</div>}
                  </div>
                </div>
              </div>
            )}

            {/* VISUALIZE */}
            {activeTab === 'visualize' && (
              <div className={cardClass}>
                <div className="flex justify-between items-center mb-5">
                  <div>
                    <div className="text-xl font-semibold">Live Visualizer</div>
                    <div className="text-xs text-zinc-400">State + ASCII + SSE updates</div>
                  </div>
                  <div className="flex gap-2">
                    <input value={vizKey} onChange={e => setVizKey(e.target.value)} className={inputClass + ' w-56'} placeholder="key" />
                    <button onClick={loadVisualize} className={buttonClass}>Load</button>
                    <button onClick={isLive ? stopLiveViz : startLiveViz} className={`${buttonClass} ${isLive ? 'bg-rose-600 hover:bg-rose-700' : ''}`}>
                      {isLive ? 'Stop SSE' : 'Start Live SSE'}
                    </button>
                  </div>
                </div>
                {vizData ? (
                  <div className="space-y-4">
                    <pre className="bg-black/70 p-5 rounded-2xl text-emerald-300 text-[12px] overflow-auto leading-tight font-mono whitespace-pre border border-zinc-800">{(vizData.current || vizData).diagram || JSON.stringify(vizData, null, 2)}</pre>
                    <div className="text-xs text-zinc-500">Use “Start Live SSE” for continuous updates from the server.</div>
                  </div>
                ) : <div className="p-12 text-center text-zinc-500 border border-dashed border-zinc-800 rounded-2xl">Enter key and load visualization.</div>}
              </div>
            )}

            {/* SIMULATE */}
            {activeTab === 'simulate' && (
              <div className={cardClass}>
                <h3 className="text-xl font-semibold mb-4">What-if Simulator</h3>
                <div className="flex flex-wrap gap-3 items-end">
                  <div className="flex-1 min-w-[180px]"><label className="text-xs">Key</label><input value={simForm.key} onChange={e=>setSimForm({...simForm,key:e.target.value})} className={inputClass} /></div>
                  <div><label className="text-xs">Max</label><input type="number" value={simForm.max_tokens} onChange={e=>setSimForm({...simForm,max_tokens:+e.target.value})} className={inputClass + ' w-20'} /></div>
                  <div><label className="text-xs">Window</label><input type="number" value={simForm.window_seconds} onChange={e=>setSimForm({...simForm,window_seconds:+e.target.value})} className={inputClass + ' w-20'} /></div>
                  <div className="flex-1 min-w-[220px]"><label className="text-xs">Costs (comma)</label><input value={simForm.costs} onChange={e=>setSimForm({...simForm,costs:e.target.value})} className={inputClass} /></div>
                  <button onClick={handleSimulate} className={buttonClass}><Play className="w-4 h-4" /> SIMULATE</button>
                </div>
                {simResults?.results && (
                  <div className="mt-6">
                    <div className="mb-2 text-xs text-zinc-400">Results</div>
                    <div className="grid gap-2">
                      {simResults.results.map((r: any, i: number) => (
                        <div key={i} className="flex items-center gap-4 bg-zinc-950 rounded-xl p-3 text-sm border border-zinc-800">
                          <div className="w-12 font-mono text-zinc-400">cost {r.cost}</div>
                          <div className={`font-semibold ${r.allowed ? 'text-emerald-400' : 'text-rose-400'}`}>{r.allowed ? 'ALLOWED' : 'LIMITED'}</div>
                          <div className="text-zinc-400">remaining {r.remaining}</div>
                          <div className="flex-1 h-1 bg-zinc-800 rounded"><div className="h-1 bg-emerald-500 rounded" style={{width: Math.min(100, (r.remaining / (simForm.max_tokens || 100)) * 100) + '%'}} /></div>
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )}

            {/* POLICIES */}
            {activeTab === 'policies' && (
              <div>
                <div className="flex items-center justify-between mb-4">
                  <div className="text-xl font-semibold">Policy Engine</div>
                  <button onClick={loadPolicies} className={buttonClass}><RefreshCw className="w-4" /> Refresh</button>
                </div>
                <div className={cardClass + ' mb-6'}>
                  {policies.length ? policies.map((p,i) => (
                    <div key={i} className="py-3 border-b border-zinc-800 flex justify-between items-center text-sm last:border-b-0">
                      <div><span className="font-medium">{p.name}</span> <span className="text-zinc-500">({p.pattern})</span></div>
                      <div className="text-xs text-zinc-400 font-mono">{p.config?.algorithm} • prio {p.priority}</div>
                    </div>
                  )) : <div className="text-sm py-6 text-center text-zinc-500">No policies. Add one below.</div>}
                </div>

                <div className={cardClass}>
                  <div className="font-medium mb-3">Add Policy</div>
                  <div className="grid grid-cols-1 md:grid-cols-5 gap-3">
                    <input placeholder="name" value={newPolicy.name} onChange={e=>setNewPolicy({...newPolicy,name:e.target.value})} className={inputClass} />
                    <input placeholder="pattern" value={newPolicy.pattern} onChange={e=>setNewPolicy({...newPolicy,pattern:e.target.value})} className={inputClass} />
                    <input placeholder="labels json" value={newPolicy.labels} onChange={e=>setNewPolicy({...newPolicy,labels:e.target.value})} className={inputClass} />
                    <input type="number" value={newPolicy.priority} onChange={e=>setNewPolicy({...newPolicy,priority:+e.target.value})} className={inputClass} />
                    <button onClick={addPolicy} className={buttonClass + ' justify-center'}>ADD</button>
                  </div>
                </div>
              </div>
            )}

            {/* REPLICATION */}
            {activeTab === 'replication' && (
              <div className={cardClass}>
                <div className="text-xl font-semibold mb-4">Cross-Node Replication</div>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                  <div className="space-y-3">
                    <input value={replicationEvent.key} onChange={e=>setReplicationEvent({...replicationEvent, key:e.target.value})} className={inputClass} placeholder="key" />
                    <textarea value={replicationEvent.value} onChange={e=>setReplicationEvent({...replicationEvent, value:e.target.value})} className={inputClass + ' h-20 font-mono'} placeholder='{"value": 42}' />
                    <button onClick={emitReplication} className={buttonClass}>Emit Replication Event</button>
                  </div>
                  <div>
                    <button onClick={loadReplicated} className="text-xs mb-2 underline">Load current state</button>
                    <pre className="bg-zinc-950 p-4 rounded-xl text-xs overflow-auto border border-zinc-800">{replicatedState ? JSON.stringify(replicatedState, null, 2) : '—'}</pre>
                  </div>
                </div>
              </div>
            )}

            {/* CLUSTER */}
            {activeTab === 'cluster' && (
              <div className={cardClass}>
                <div className="flex justify-between">
                  <div className="text-xl font-semibold">Cluster Registry</div>
                  <button onClick={loadClusterViz} className={buttonClass}>Refresh Nodes</button>
                </div>
                <div className="mt-5 text-sm">
                  <div className="text-xs text-zinc-400 mb-1">NODES</div>
                  <pre className="bg-black p-4 rounded-xl border border-zinc-800">{JSON.stringify(clusterData || {nodes: [health?.node || 'local']}, null, 2)}</pre>
                  <div className="text-xs text-zinc-500 mt-2">Uses Redis set + heartbeat for discovery (two-tier resilient mode)</div>
                </div>
              </div>
            )}

            {/* REPLAY */}
            {activeTab === 'replay' && (
              <div className={cardClass}>
                <h3 className="font-semibold mb-4">Replay Decision Log</h3>
                <div className="flex gap-3 flex-wrap mb-4">
                  <input type="number" value={replayReq.from_ts} onChange={e=>setReplayReq({...replayReq,from_ts:+e.target.value})} className={inputClass + ' w-44'} />
                  <input type="number" value={replayReq.to_ts} onChange={e=>setReplayReq({...replayReq,to_ts:+e.target.value})} className={inputClass + ' w-44'} />
                  <input value={replayReq.key} onChange={e=>setReplayReq({...replayReq,key:e.target.value})} placeholder="optional key" className={inputClass + ' flex-1'} />
                  <button onClick={runReplay} className={buttonClass}>REPLAY</button>
                </div>
                {replayResults && <pre className="bg-zinc-950 p-4 text-xs rounded-xl overflow-auto">{JSON.stringify(replayResults, null, 2)}</pre>}
              </div>
            )}

            {/* ADMIN */}
            {activeTab === 'admin' && (
              <div className={cardClass}>
                <h3 className="font-semibold mb-3">Admin Operations</h3>
                <div className="flex gap-3 mb-4">
                  <input value={adminKey} onChange={e=>setAdminKey(e.target.value)} className={inputClass + ' flex-1'} />
                  <button onClick={doInspect} className={buttonClass}>Inspect</button>
                  <button onClick={doReset} className="px-4 py-2 bg-rose-600 hover:bg-rose-700 rounded-xl">Reset</button>
                </div>
                {adminInspect && <pre className="text-xs bg-black p-4 rounded-xl overflow-auto">{JSON.stringify(adminInspect, null, 2)}</pre>}
                <div className="text-xs text-zinc-500 mt-4">Snapshot / Restore available via direct /v1/snapshot and /v1/restore</div>
              </div>
            )}

            {/* FULL LOG TAB */}
            {activeTab === 'log' && (
              <div className={cardClass}>
                <div className="flex justify-between items-center mb-4">
                  <div>Full Activity Log ({filteredLogs.length})</div>
                  <div className="flex gap-2">
                    <button onClick={clearLog} className="text-xs px-3 py-1 rounded-xl bg-zinc-800 flex items-center gap-1"><Trash2 className="w-3.5 h-3.5" /> CLEAR</button>
                    <button onClick={() => exportLog('json')} className="text-xs px-3 py-1 rounded-xl bg-zinc-800 flex items-center gap-1"><Download className="w-3.5 h-3.5" /> JSON</button>
                    <button onClick={() => exportLog('csv')} className="text-xs px-3 py-1 rounded-xl bg-zinc-800 flex items-center gap-1"><Download className="w-3.5 h-3.5" /> CSV</button>
                  </div>
                </div>
                <input placeholder="Filter log..." value={logFilter} onChange={e=>setLogFilter(e.target.value)} className={inputClass + ' mb-3'} />
                {renderLogList()}
              </div>
            )}

            <div className="text-[10px] mt-10 text-zinc-500">All operations are live against the Go backend. Framework: React + Vite + Tailwind + Recharts.</div>
          </div>
        </div>

        {/* PERSISTENT RIGHT LOG PANEL (always visible when toggled) */}
        {showLogPanel && (
          <div className="w-80 border-l border-zinc-800 bg-zinc-900 flex flex-col h-[calc(100vh-3.5rem)] flex-shrink-0">
            <div className="px-4 py-3 border-b border-zinc-800 flex items-center justify-between shrink-0">
              <div className="font-semibold text-sm flex items-center gap-2"><History className="w-4 h-4"/> Recent Results Log</div>
              <div className="flex items-center gap-1">
                <button onClick={clearLog} title="Clear" className="p-1 rounded hover:bg-zinc-800"><Trash2 className="w-3.5 h-3.5"/></button>
                <button onClick={() => exportLog('json')} className="text-[10px] px-2 py-0.5 bg-zinc-800 hover:bg-zinc-700 rounded">JSON</button>
                <button onClick={() => exportLog('csv')} className="text-[10px] px-2 py-0.5 bg-zinc-800 hover:bg-zinc-700 rounded">CSV</button>
                <button onClick={() => setShowLogPanel(false)}><X className="w-4 h-4" /></button>
              </div>
            </div>

            <div className="px-3 pt-2 pb-1 shrink-0">
              <input value={logFilter} onChange={e => setLogFilter(e.target.value)} placeholder="Filter..." className="w-full bg-zinc-950 text-xs px-2 py-1 rounded-xl border border-zinc-800" />
            </div>

            {renderLogList()}

            <div className="p-3 border-t border-zinc-800 bg-zinc-950 shrink-0">
              <div className="text-xs font-medium mb-2 tracking-wider text-zinc-400">CHARTS FROM LOG</div>
              <div className="h-28 mb-3">
                <ResponsiveContainer>
                  <BarChart data={activityData.length ? activityData : [{name:'check', value:1}] }>
                    <Bar dataKey="value" fill="#6366f1" />
                    <Tooltip />
                  </BarChart>
                </ResponsiveContainer>
              </div>
              <div className="text-[10px] text-zinc-500">Actions logged here persist across tab switches. Use exports for audit.</div>
            </div>
          </div>
        )}
      </div>

      <div className="text-[10px] px-6 py-2 border-t border-zinc-800 bg-zinc-900 text-zinc-500 flex justify-between">
        <div>RateFlow • Framework dashboard (React)</div>
        <div>Backend: {health ? `${health.backend} @ ${health.node}` : 'connecting...'}</div>
      </div>
    </div>
  );
};

export default App;
