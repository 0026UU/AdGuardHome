import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';

import { AGGREGATE_NODE } from '../../helpers/cluster';
import { useClusterStatus } from './useClusterStatus';
import './Cluster.css';

type NodeSwitcherProps = {
    value: string;
    onChange: (node: string) => void;
};

const NodeSwitcher = ({ value, onChange }: NodeSwitcherProps) => {
    const { t } = useTranslation();
    const { status, error } = useClusterStatus();

    useEffect(() => {
        if (
            status?.enabled &&
            value !== AGGREGATE_NODE &&
            !status.nodes.some((node) => node.name === value)
        ) {
            onChange(AGGREGATE_NODE);
        }
    }, [status, value, onChange]);

    if (!status && error) {
        return <div className="alert alert-danger cluster-node-switcher__error">{error}</div>;
    }
    if (!status?.enabled) {
        return null;
    }

    return (
        <div className="cluster-node-switcher">
            <div className="cluster-node-switcher__control">
                <label htmlFor="cluster-node-selector">{t('cluster.switcher.label')}</label>
                <select
                    id="cluster-node-selector"
                    className="form-control custom-select custom-select-sm"
                    value={value}
                    onChange={(event) => onChange(event.target.value)}>
                    <option value={AGGREGATE_NODE}>{t('cluster.switcher.aggregate')}</option>
                    {status.nodes.map((node) => (
                        <option key={node.name} value={node.name}>
                            {node.name}
                            {node.local ? ` (${t('cluster.nodes.local')})` : ''}
                            {!node.online ? ` — ${t('cluster.nodes.offline')}` : ''}
                        </option>
                    ))}
                </select>
            </div>
            {status.degraded_nodes > 0 && (
                <div className="cluster-node-switcher__warning" role="status">
                    {t('cluster.switcher.degraded')}
                </div>
            )}
        </div>
    );
};

export default NodeSwitcher;
