import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import apiClient from '../../api/Api';
import { CLUSTER_POLL_INTERVAL, ClusterStatus, getClusterErrorKey } from '../../helpers/cluster';

export const useClusterStatus = (poll = true) => {
    const { t } = useTranslation();
    const mounted = useRef(false);
    const [status, setStatus] = useState<ClusterStatus | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState('');

    const reload = useCallback(async () => {
        try {
            const next = await apiClient.getClusterStatus();
            if (!mounted.current) {
                return;
            }
            setStatus(next);
            setError('');
        } catch (requestError) {
            if (mounted.current) {
                setError(
                    t(getClusterErrorKey(requestError), {
                        defaultValue: t('cluster.errors.unknown'),
                    }),
                );
            }
        } finally {
            if (mounted.current) {
                setLoading(false);
            }
        }
    }, [t]);

    useEffect(() => {
        mounted.current = true;
        reload();
        const timer = poll ? window.setInterval(reload, CLUSTER_POLL_INTERVAL) : undefined;

        return () => {
            mounted.current = false;
            if (timer !== undefined) {
                window.clearInterval(timer);
            }
        };
    }, [poll, reload]);

    return { status, loading, error, reload };
};
