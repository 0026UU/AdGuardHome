import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import apiClient from '../api/Api';
import i18n from '../i18n';

const jsonResponse = (data: unknown, status = 200) =>
    new Response(JSON.stringify(data), {
        status,
        headers: { 'Content-Type': 'application/json' },
    });

describe('cluster API routing', () => {
    beforeEach(() => {
        window.localStorage.clear();
        vi.stubGlobal('fetch', vi.fn().mockImplementation(async () => jsonResponse({})));
    });

    afterEach(() => {
        window.localStorage.clear();
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
    });

    test('routes supported requests to the persisted node and preserves queries', async () => {
        window.localStorage.setItem('agh_cluster_node', 'member');

        await apiClient.makeRequest('querylog?limit=20', 'GET');

        expect(fetch).toHaveBeenCalledWith('control/querylog?limit=20&agh_node=member', expect.anything());
    });

    test('does not route cluster management or aggregate requests', async () => {
        window.localStorage.setItem('agh_cluster_node', 'member');
        await apiClient.makeRequest('cluster/ui/status', 'GET');
        window.localStorage.setItem('agh_cluster_node', 'all');
        await apiClient.makeRequest('stats', 'GET');

        expect(vi.mocked(fetch).mock.calls.map(([url]) => url)).toStrictEqual([
            'control/cluster/ui/status',
            'control/stats',
        ]);
    });

    test('maps structured node errors to localized messages', async () => {
        vi.mocked(fetch).mockResolvedValue(jsonResponse({ error: 'node_offline' }, 503));

        await expect(apiClient.makeRequest('stats', 'GET')).rejects.toMatchObject({
            code: 'node_offline',
            message: i18n.t('cluster.errors.node_offline'),
        });
    });

    test('preserves non-cluster backend errors', async () => {
        vi.mocked(fetch).mockResolvedValue(jsonResponse({ error: 'invalid filter' }, 400));

        await expect(apiClient.makeRequest('filtering/status', 'GET')).rejects.toThrow('control/filtering/status');
    });
});
