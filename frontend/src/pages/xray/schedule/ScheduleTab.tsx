import { CloudDownloadOutlined, ReloadOutlined, HistoryOutlined } from '@ant-design/icons';
import { Alert, ConfigProvider, Tabs } from 'antd';
import arEG from 'antd/locale/ar_EG';
import enUS from 'antd/locale/en_US';
import esES from 'antd/locale/es_ES';
import faIR from 'antd/locale/fa_IR';
import idID from 'antd/locale/id_ID';
import jaJP from 'antd/locale/ja_JP';
import ptBR from 'antd/locale/pt_BR';
import ruRU from 'antd/locale/ru_RU';
import trTR from 'antd/locale/tr_TR';
import ukUA from 'antd/locale/uk_UA';
import viVN from 'antd/locale/vi_VN';
import zhCN from 'antd/locale/zh_CN';
import zhTW from 'antd/locale/zh_TW';
import { useTranslation } from 'react-i18next';

import { useMediaQuery } from '@/hooks/useMediaQuery';
import GeodataSection from '@/pages/index/GeodataSection';
import { catTabLabel } from '@/pages/settings/catTabLabel';
import RestartScheduleSection from './RestartScheduleSection';
import ExecutionLogsSection from './ExecutionLogsSection';

const locales: Record<string, typeof enUS> = {
  'ar-EG': arEG,
  'en-US': enUS,
  'es-ES': esES,
  'fa-IR': faIR,
  'id-ID': idID,
  'ja-JP': jaJP,
  'pt-BR': ptBR,
  'ru-RU': ruRU,
  'tr-TR': trTR,
  'uk-UA': ukUA,
  'vi-VN': viVN,
  'zh-CN': zhCN,
  'zh-TW': zhTW,
};

export default function ScheduleTab({ templateDirty }: { templateDirty: boolean }) {
  const { t, i18n } = useTranslation();
  const { isMobile } = useMediaQuery();
  return (
    <ConfigProvider
      locale={locales[i18n.language] ?? enUS}
      direction={['ar-EG', 'fa-IR'].includes(i18n.language) ? 'rtl' : 'ltr'}
    >
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
          {
            key: 'logs',
            label: catTabLabel(<HistoryOutlined />, t('pages.xray.schedule.logsTitle'), isMobile),
            children: <ExecutionLogsSection />,
          },
        ]}
      />
    </ConfigProvider>
  );
}
