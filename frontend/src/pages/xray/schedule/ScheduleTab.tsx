import { Alert, Card, Space } from 'antd';
import { useTranslation } from 'react-i18next';

import GeodataSection from '@/pages/index/GeodataSection';
import RestartScheduleSection from './RestartScheduleSection';

export default function ScheduleTab({ templateDirty }: { templateDirty: boolean }) {
  const { t } = useTranslation();
  return (
    <Space orientation="vertical" size="large" style={{ width: '100%' }}>
      <Card title={t('pages.xray.schedule.restartTitle')}>
        <RestartScheduleSection />
      </Card>
      <Card title={t('pages.xray.schedule.geoTitle')}>
        {templateDirty && (
          <Alert
            type="warning"
            showIcon
            className="mb-12"
            title={t('pages.xray.schedule.unsavedTemplateHint')}
          />
        )}
        <GeodataSection active saveDisabled={templateDirty} />
      </Card>
    </Space>
  );
}
