import React, { FormEvent, useState } from 'react';
import { useTranslation } from 'react-i18next';

import apiClient from '../../api/Api';
import { getClusterErrorKey } from '../../helpers/cluster';
import Card from '../ui/Card';
import Loading from '../ui/Loading';
import PageTitle from '../ui/PageTitle';
import ClusterNav from './ClusterNav';
import { useClusterStatus } from './useClusterStatus';
import './Cluster.css';

const initialRegistration = {
    name: '',
    api_base: '',
    token: '',
    timeout: '5',
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
};

const Nodes = () => {
    const { t, i18n } = useTranslation();
    const { status, loading, error, reload } = useClusterStatus();
    const [registration, setRegistration] = useState(initialRegistration);
    const [processing, setProcessing] = useState(false);
    const [action, setAction] = useState('');
    const [actionError, setActionError] = useState('');
    const [notice, setNotice] = useState('');

    const localizeError = (requestError: unknown) =>
        t(getClusterErrorKey(requestError), { defaultValue: t('cluster.errors.unknown') });

    const updateRegistration = (field: string, value: string) => {
        setRegistration((current) => ({ ...current, [field]: value }));
    };

    const register = async (event: FormEvent) => {
        event.preventDefault();
        setProcessing(true);
        setActionError('');
        setNotice('');
        try {
            await apiClient.registerClusterNode({
                ...registration,
                timeout: `${registration.timeout}s`,
            });
            setRegistration(initialRegistration);
            setNotice(t('cluster.nodes.registered'));
            await reload();
        } catch (requestError) {
            setActionError(localizeError(requestError));
        } finally {
            setProcessing(false);
        }
    };

    const unregister = async (name: string) => {
        if (!window.confirm(t('cluster.nodes.unregister_confirm', { name }))) {
            return;
        }
        setAction(name);
        setActionError('');
        setNotice('');
        try {
            await apiClient.unregisterClusterNode(name);
            setNotice(t('cluster.nodes.unregistered'));
            await reload();
        } catch (requestError) {
            setActionError(localizeError(requestError));
        } finally {
            setAction('');
        }
    };

    const align = async (name: string) => {
        setAction(name);
        setActionError('');
        setNotice('');
        try {
            await apiClient.alignCluster(name);
            setNotice(t('cluster.nodes.alignment_finished'));
            await reload();
        } catch (requestError) {
            setActionError(localizeError(requestError));
        } finally {
            setAction('');
        }
    };

    const formatHeartbeat = (value: string) => {
        if (!value || value.startsWith('0001-')) {
            return t('cluster.nodes.never');
        }
        const date = new Date(value);
        if (Number.isNaN(date.getTime())) {
            return t('cluster.nodes.unknown_time');
        }

        return new Intl.DateTimeFormat(i18n.language, {
            dateStyle: 'short',
            timeStyle: 'medium',
        }).format(date);
    };

    return (
        <div className="cluster-page">
            <PageTitle title={t('cluster.nodes.title')} subtitle={t('cluster.nodes.subtitle')} />
            <ClusterNav />

            {loading && <Loading />}
            {error && <div className="alert alert-danger">{error}</div>}
            {actionError && <div className="alert alert-danger">{actionError}</div>}
            {notice && <div className="alert alert-success">{notice}</div>}
            {status && !status.enabled && <div className="alert alert-warning">{t('cluster.disabled')}</div>}
            {status?.enabled && status.mode !== 'master' && (
                <div className="alert alert-warning">{t('cluster.master_only')}</div>
            )}

            {status?.enabled && status.mode === 'master' && (
                <Card title={t('cluster.nodes.register_title')} subtitle={t('cluster.nodes.register_subtitle')}>
                    <form onSubmit={register}>
                        <div className="cluster-form-grid">
                            <div className="form-group">
                                <label className="form__label" htmlFor="cluster-node-name">
                                    {t('cluster.nodes.name')}
                                </label>
                                <input
                                    id="cluster-node-name"
                                    className="form-control"
                                    value={registration.name}
                                    onChange={(event) => updateRegistration('name', event.target.value)}
                                    placeholder={t('cluster.nodes.name_placeholder')}
                                    required
                                />
                            </div>
                            <div className="form-group">
                                <label className="form__label" htmlFor="cluster-node-address">
                                    {t('cluster.nodes.api_base')}
                                </label>
                                <input
                                    id="cluster-node-address"
                                    className="form-control"
                                    type="url"
                                    value={registration.api_base}
                                    onChange={(event) => updateRegistration('api_base', event.target.value)}
                                    placeholder={t('cluster.nodes.api_base_placeholder')}
                                    required
                                />
                            </div>
                            <div className="form-group">
                                <label className="form__label" htmlFor="cluster-node-token">
                                    {t('cluster.nodes.token')}
                                </label>
                                <input
                                    id="cluster-node-token"
                                    className="form-control"
                                    type="password"
                                    autoComplete="new-password"
                                    value={registration.token}
                                    onChange={(event) => updateRegistration('token', event.target.value)}
                                    required
                                />
                                <small className="form-text text-muted">{t('cluster.nodes.token_hint')}</small>
                            </div>
                            <div className="form-group">
                                <label className="form__label" htmlFor="cluster-node-timeout">
                                    {t('cluster.nodes.timeout')}
                                </label>
                                <input
                                    id="cluster-node-timeout"
                                    className="form-control"
                                    type="number"
                                    min="1"
                                    max="300"
                                    value={registration.timeout}
                                    onChange={(event) => updateRegistration('timeout', event.target.value)}
                                    required
                                />
                            </div>
                            <div className="form-group cluster-form-grid__wide">
                                <label className="form__label" htmlFor="cluster-node-timezone">
                                    {t('cluster.nodes.timezone')}
                                </label>
                                <input
                                    id="cluster-node-timezone"
                                    className="form-control"
                                    value={registration.timezone}
                                    onChange={(event) => updateRegistration('timezone', event.target.value)}
                                    required
                                />
                            </div>
                        </div>
                        <button type="submit" className="btn btn-success" disabled={processing}>
                            {processing ? t('cluster.nodes.registering') : t('cluster.nodes.register')}
                        </button>
                    </form>
                </Card>
            )}

            {status?.enabled && (
                <Card title={t('cluster.nodes.list_title')}>
                    <div className="table-responsive">
                        <table className="table table-vcenter cluster-table">
                            <thead>
                                <tr>
                                    <th>{t('cluster.nodes.name')}</th>
                                    <th>{t('cluster.nodes.health')}</th>
                                    <th>{t('cluster.nodes.api_base')}</th>
                                    <th>{t('cluster.nodes.rtt')}</th>
                                    <th>{t('cluster.nodes.version')}</th>
                                    <th>{t('cluster.nodes.heartbeat')}</th>
                                    <th>{t('cluster.nodes.pending')}</th>
                                    <th>{t('cluster.nodes.actions')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {status.nodes.map((node) => (
                                    <tr key={node.name}>
                                        <td>
                                            <strong>{node.name}</strong>
                                            {node.local && (
                                                <span className="badge badge-secondary cluster-badge">
                                                    {t('cluster.nodes.local')}
                                                </span>
                                            )}
                                        </td>
                                        <td>
                                            <span className={`badge ${node.online ? 'badge-success' : 'badge-danger'}`}>
                                                {node.online ? t('cluster.nodes.online') : t('cluster.nodes.offline')}
                                            </span>
                                            {node.degraded && (
                                                <span className="badge badge-warning">{t('cluster.nodes.degraded')}</span>
                                            )}
                                        </td>
                                        <td className="cluster-table__endpoint" title={node.api_base}>
                                            {node.api_base}
                                        </td>
                                        <td>{t('cluster.nodes.rtt_value', { value: node.rtt_ms })}</td>
                                        <td>{node.version || t('cluster.nodes.unknown_version')}</td>
                                        <td>{formatHeartbeat(node.last_heartbeat)}</td>
                                        <td>{node.pending_sync}</td>
                                        <td>
                                            {!node.local && status.mode === 'master' && (
                                                <div className="cluster-actions">
                                                    <button
                                                        type="button"
                                                        className="btn btn-outline-primary btn-sm"
                                                        disabled={action === node.name || !node.online}
                                                        onClick={() => align(node.name)}>
                                                        {t('cluster.nodes.align')}
                                                    </button>
                                                    <button
                                                        type="button"
                                                        className="btn btn-outline-danger btn-sm"
                                                        disabled={action === node.name}
                                                        onClick={() => unregister(node.name)}>
                                                        {t('cluster.nodes.unregister')}
                                                    </button>
                                                </div>
                                            )}
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                </Card>
            )}
        </div>
    );
};

export default Nodes;
