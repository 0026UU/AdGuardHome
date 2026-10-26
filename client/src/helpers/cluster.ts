export const CLUSTER_NODE_STORAGE_KEY = 'agh_cluster_node';
export const AGGREGATE_NODE = 'all';
export const CLUSTER_POLL_INTERVAL = 10000;

export type ClusterNode = {
    name: string;
    api_base: string;
    online: boolean;
    last_heartbeat: string;
    rtt_ms: number;
    version: string;
    pending_sync: number;
    degraded: boolean;
    local: boolean;
};

export type ClusterStatus = {
    enabled: boolean;
    mode: 'master' | 'member';
    name: string;
    degraded_nodes: number;
    nodes: ClusterNode[];
    config: {
        sync: {
            enabled: boolean;
            interval: string;
            on_change: string;
            conflict: 'master_wins' | 'reject';
        };
        stats: {
            window: 'min' | 'align_to_master';
        };
        degradation: {
            skip_offline: boolean;
        };
    };
};

export type ClusterAuditEntry = {
    time: string;
    operation: string;
    source: string;
    target: string;
    result: string;
    message?: string;
};

const nodeRoutablePaths = new Set([
    'stats',
    'stats/config',
    'querylog',
    'querylog/config',
    'clients',
    'clients/search',
    'clients/find',
    'profile',
    'dns_info',
    'access/list',
    'filtering/status',
    'rewrite/list',
    'rewrite/settings',
    'blocked_services/get',
    'safesearch/status',
    'safebrowsing/status',
    'parental/status',
    'tls/status',
    'dhcp/status',
    'filtering/config',
    'filtering/add_url',
    'filtering/remove_url',
    'filtering/set_url',
    'filtering/set_rules',
    'rewrite/add',
    'rewrite/delete',
    'rewrite/update',
    'rewrite/settings/update',
    'blocked_services/set',
    'blocked_services/update',
    'safebrowsing/enable',
    'safebrowsing/disable',
    'parental/enable',
    'parental/disable',
    'safesearch/enable',
    'safesearch/disable',
    'safesearch/settings',
    'dns_config',
    'protection',
    'access/set',
    'stats/config/update',
    'stats_config',
    'querylog/config/update',
    'querylog_config',
    'clients/add',
    'clients/delete',
    'clients/update',
    'profile/update',
    'tls/configure',
    'dhcp/set_config',
    'dhcp/add_static_lease',
    'dhcp/remove_static_lease',
    'dhcp/update_static_lease',
]);

export const getStoredClusterNode = () => {
    try {
        return window.localStorage.getItem(CLUSTER_NODE_STORAGE_KEY) || AGGREGATE_NODE;
    } catch (_error) {
        return AGGREGATE_NODE;
    }
};

export const setStoredClusterNode = (node: string) => {
    try {
        window.localStorage.setItem(CLUSTER_NODE_STORAGE_KEY, node);
    } catch (_error) {
        // The current selection still works for this page when storage is unavailable.
    }
};

export const withSelectedClusterNode = (path: string, selectedNode = getStoredClusterNode()) => {
    const [pathname] = path.split('?');
    if (selectedNode === AGGREGATE_NODE || !nodeRoutablePaths.has(pathname)) {
        return path;
    }

    const separator = path.includes('?') ? '&' : '?';
    return `${path}${separator}agh_node=${encodeURIComponent(selectedNode)}`;
};

export const getClusterErrorKey = (error: any) => {
    const code = error?.code || error?.response?.data?.error;
    if (typeof code === 'string' && /^[a-z0-9_]+$/.test(code)) {
        return `cluster.errors.${code}`;
    }

    return 'cluster.errors.unknown';
};
