import React from 'react';
import { NavLink } from 'react-router-dom';
import { useTranslation } from 'react-i18next';

import { CLUSTER_URLS } from '../../helpers/constants';

const ClusterNav = () => {
    const { t } = useTranslation();

    return (
        <ul className="nav nav-tabs mb-4">
            <li className="nav-item">
                <NavLink className="nav-link" to={CLUSTER_URLS.nodes}>
                    {t('cluster.nodes.nav')}
                </NavLink>
            </li>
            <li className="nav-item">
                <NavLink className="nav-link" to={CLUSTER_URLS.settings}>
                    {t('cluster.settings.nav')}
                </NavLink>
            </li>
        </ul>
    );
};

export default ClusterNav;
