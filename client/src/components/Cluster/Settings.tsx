import React, { FormEvent, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import apiClient from '../../api/Api';
import {
    CLUSTER_POLL_INTERVAL,
    ClusterAuditEntry,
    getClusterErrorKey,
} from '../../helpers/cluster';
import Card from '../ui/Card';
import Loading from '../ui/Loading';
import PageTitle from '../ui/PageTitle';
import { Checkbox } from '../ui/Controls/Checkbox';
import ClusterNav from './ClusterNav';
import { useClusterStatus } from './useClusterStatus';
import './Cluster.css';

type SettingsForm = {
    syncEnabled: boolean;
    interval: number;
    conflict: string;
    statsWindow: string;
    skipOffline: boolean;
};

const auditOperations = new Set(['write', 'register', 'unregister', 'align']);
const auditResults = new Set(['queued', 'success', 'failed', 'skipped', 'completed']);
const auditMessages = new Set([
    'completed',
    'partial_failure',
    'queue_failed',
    'request_failed',
    'registered',
    'unregistered',
    'tls_credentials_unavailable',
    'conflict_rejected',
    'read_master_failed',
    'read_target_failed',
    'invalid_master_response',
    'invalid_target_response',
    'encode_failed',
    'apply_failed',
    'node_removed',
    'retry_exhausted',
    'retry_scheduled',
]);

const durationToSeconds = (duration: string) => {
    const match = /^(\d+(?:\.\d+)?)(s|m|h)$/.exec(duration);
    if (!match) {
        return 30;
    }
    let multiplier = 1;
    if (match[2] === 'h') {
        multiplier = 3600;
    } else if (match[2] === 'm') {
        multiplier = 60;
    }
    return Number(match[1]) * multiplier;
};

const Settings = () => {
    const { t, i18n } = useTranslation();
    const { status, loading, error, reload } = useClusterStatus(false);
    const [form, setForm] = useState<SettingsForm | null>(null);
    const [processing, setProcessing] = useState(false);
    const [aligning, setAligning] = useState(false);
    const [actionError, setActionError] = useState('');
    const [notice, setNotice] = useState('');
    const [audit, setAudit] = useState<ClusterAuditEntry[]>([]);

    const localizeError = useCallback(
        (requestError: unknown) =>
            t(getClusterErrorKey(requestError), { defaultValue: t('cluster.errors.unknown') }),
        [t],
    );

    const loadAudit = useCallback(async () => {
        try {
            const data = await apiClient.getClusterAudit();
            setAudit(data.entries || []);
        } catch (requestError) {
            setActionError(localizeError(requestError));
        }
    }, [localizeError]);

    useEffect(() => {
        if (!status?.config || form) {
            return;
        }
        setForm({
            syncEnabled: status.config.sync.enabled,
            interval: durationToSeconds(status.config.sync.interval),
            conflict: status.config.sync.conflict,
            statsWindow: status.config.stats.window,
            skipOffline: status.config.degradation.skip_offline,
        });
    }, [status, form]);

    useEffect(() => {
        let active = true;
        const load = async () => {
            if (active) {
                await loadAudit();
            }
        };
        load();
        const timer = window.setInterval(load, CLUSTER_POLL_INTERVAL);

        return () => {
            active = false;
            window.clearInterval(timer);
        };
    }, [loadAudit]);

    const save = async (event: FormEvent) => {
        event.preventDefault();
        if (!form || !status?.config) {
            return;
        }
        setProcessing(true);
        setActionError('');
        setNotice('');
        try {
            await apiClient.updateClusterSettings({
                sync: {
                    enabled: form.syncEnabled,
                    interval: `${form.interval}s`,
                    on_change: status.config.sync.on_change,
                    conflict: form.conflict,
                },
                stats: { window: form.statsWindow },
                degradation: { skip_offline: form.skipOffline },
            });
            setNotice(t('cluster.settings.saved'));
            await reload();
        } catch (requestError) {
            setActionError(localizeError(requestError));
        } finally {
            setProcessing(false);
        }
    };

    const alignAll = async () => {
        setAligning(true);
        setActionError('');
        setNotice('');
        try {
            await apiClient.alignCluster();
            setNotice(t('cluster.settings.alignment_finished'));
            await Promise.all([reload(), loadAudit()]);
        } catch (requestError) {
            setActionError(localizeError(requestError));
        } finally {
            setAligning(false);
        }
    };

    const updateForm = (field: keyof SettingsForm, value: string | number | boolean) => {
        setForm((current) => (current ? { ...current, [field]: value } : current));
    };

    const formatTime = (value: string) =>
        new Intl.DateTimeFormat(i18n.language, { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value));

    const translateAudit = (type: 'operation' | 'result' | 'message', value: string) => {
        if (!value) {
            return '—';
        }
        if (type === 'message' && /^remote_status_\d+$/.test(value)) {
            return t('cluster.audit.message.remote_status', { status: value.slice('remote_status_'.length) });
        }
        let known = auditMessages.has(value);
        if (type === 'operation') {
            known = auditOperations.has(value);
        } else if (type === 'result') {
            known = auditResults.has(value);
        }

        return known
            ? t(`cluster.audit.${type}.${value}`)
            : t(`cluster.audit.${type}.unknown`);
    };

    return (
        <div className="cluster-page">
            <PageTitle title={t('cluster.settings.title')} subtitle={t('cluster.settings.subtitle')} />
            <ClusterNav />

            {loading && <Loading />}
            {error && <div className="alert alert-danger">{error}</div>}
            {actionError && <div className="alert alert-danger">{actionError}</div>}
            {notice && <div className="alert alert-success">{notice}</div>}
            {status && !status.enabled && <div className="alert alert-warning">{t('cluster.disabled')}</div>}
            {status?.enabled && status.mode !== 'master' && (
                <div className="alert alert-warning">{t('cluster.master_only')}</div>
            )}

            {status?.enabled && status.mode === 'master' && form && (
                <>
                    <Card title={t('cluster.settings.sync_title')}>
                        <form onSubmit={save}>
                            <div className="cluster-settings-section">
                                <Checkbox
                                    title={t('cluster.settings.sync_enabled')}
                                    subtitle={t('cluster.settings.sync_enabled_desc')}
                                    value={form.syncEnabled}
                                    onChange={(value) => updateForm('syncEnabled', value)}
                                />
                            </div>
                            <div className="cluster-form-grid">
                                <div className="form-group">
                                    <label className="form__label" htmlFor="cluster-sync-interval">
                                        {t('cluster.settings.interval')}
                                    </label>
                                    <input
                                        id="cluster-sync-interval"
                                        className="form-control"
                                        type="number"
                                        min="1"
                                        max="86400"
                                        value={form.interval}
                                        onChange={(event) => updateForm('interval', Number(event.target.value))}
                                        required
                                    />
                                    <small className="form-text text-muted">{t('cluster.settings.interval_desc')}</small>
                                </div>
                                <div className="form-group">
                                    <label className="form__label" htmlFor="cluster-conflict">
                                        {t('cluster.settings.conflict')}
                                    </label>
                                    <select
                                        id="cluster-conflict"
                                        className="form-control custom-select"
                                        value={form.conflict}
                                        onChange={(event) => updateForm('conflict', event.target.value)}>
                                        <option value="master_wins">{t('cluster.settings.master_wins')}</option>
                                        <option value="reject">{t('cluster.settings.reject')}</option>
                                    </select>
                                </div>
                                <div className="form-group">
                                    <label className="form__label" htmlFor="cluster-stats-window">
                                        {t('cluster.settings.stats_window')}
                                    </label>
                                    <select
                                        id="cluster-stats-window"
                                        className="form-control custom-select"
                                        value={form.statsWindow}
                                        onChange={(event) => updateForm('statsWindow', event.target.value)}>
                                        <option value="min">{t('cluster.settings.stats_min')}</option>
                                        <option value="align_to_master">{t('cluster.settings.stats_master')}</option>
                                    </select>
                                </div>
                            </div>
                            <div className="cluster-settings-section">
                                <Checkbox
                                    title={t('cluster.settings.skip_offline')}
                                    subtitle={t('cluster.settings.skip_offline_desc')}
                                    value={form.skipOffline}
                                    onChange={(value) => updateForm('skipOffline', value)}
                                />
                            </div>
                            <button type="submit" className="btn btn-success" disabled={processing}>
                                {processing ? t('cluster.settings.saving') : t('save_btn')}
                            </button>
                        </form>
                    </Card>

                    <Card title={t('cluster.settings.align_title')} subtitle={t('cluster.settings.align_desc')}>
                        <button type="button" className="btn btn-outline-primary" onClick={alignAll} disabled={aligning}>
                            {aligning ? t('cluster.settings.aligning') : t('cluster.settings.align_all')}
                        </button>
                    </Card>
                </>
            )}

            {status?.enabled && (
                <Card title={t('cluster.audit.title')}>
                    <div className="table-responsive cluster-audit">
                        <table className="table table-vcenter cluster-table">
                            <thead>
                                <tr>
                                    <th>{t('cluster.audit.time')}</th>
                                    <th>{t('cluster.audit.operation_label')}</th>
                                    <th>{t('cluster.audit.source')}</th>
                                    <th>{t('cluster.audit.target')}</th>
                                    <th>{t('cluster.audit.result_label')}</th>
                                    <th>{t('cluster.audit.message_label')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {audit.length === 0 && (
                                    <tr>
                                        <td colSpan={6}>{t('cluster.audit.empty')}</td>
                                    </tr>
                                )}
                                {[...audit].reverse().map((entry, index) => (
                                    <tr key={`${entry.time}-${index}`}>
                                        <td>{formatTime(entry.time)}</td>
                                        <td>{translateAudit('operation', entry.operation)}</td>
                                        <td>{entry.source}</td>
                                        <td>{entry.target === 'all' ? t('cluster.switcher.aggregate') : entry.target}</td>
                                        <td>{translateAudit('result', entry.result)}</td>
                                        <td>{translateAudit('message', entry.message || '')}</td>
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

export default Settings;
