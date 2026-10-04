import { CloudDownloadOutlined, ReloadOutlined } from '@ant-design/icons';
import { Alert, Tabs } from 'antd';
import { useTranslation } from 'react-i18next';

import { useMediaQuery } from '@/hooks/useMediaQuery';
import GeodataSection from '@/pages/index/GeodataSection';
import { catTabLabel } from '@/pages/settings/catTabLabel';
import RestartScheduleSection from './RestartScheduleSection';

export default function ScheduleTab({ templateDirty }: { templateDirty: boolean }) {
  const { t } = useTranslation();
  const { isMobile } = useMediaQuery();
  return (
    <Tabs
      defaultActiveKey="restart"
      items={[
        {
          key: 'restart',
          label: catTabLabel(<ReloadOutlined />, t('pages.xray.schedule.restartTitle'), isMobile),
          children: <RestartScheduleSection />,
        },
        {
          key: 'geo',
          label: catTabLabel(
            <CloudDownloadOutlined />,
            t('pages.xray.schedule.geoTitle'),
            isMobile,
          ),
          children: (
            <>
              {templateDirty && (
                <Alert
                  type="warning"
                  showIcon
                  className="mb-12"
                  title={t('pages.xray.schedule.unsavedTemplateHint')}
                />
              )}
              <GeodataSection active saveDisabled={templateDirty} />
            </>
          ),
        },
      ]}
    />
  );
}
