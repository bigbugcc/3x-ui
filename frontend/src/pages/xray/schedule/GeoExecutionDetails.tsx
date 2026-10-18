import { useState } from 'react';
import { Button, Collapse, Modal, Space, Tag, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import type { TaskExecutionLog } from '@/generated/types';
import { formatScheduleTime } from './ScheduleEditor';

export default function GeoExecutionDetails({ record }: { record: TaskExecutionLog }) {
  const { t, i18n } = useTranslation();
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  let summary = record.details
    ? t(`pages.xray.schedule.${record.details}`, { defaultValue: record.details })
    : t(`pages.xray.schedule.${record.status}`);
  if (record.status === 'running') summary = t('pages.xray.schedule.geoUpdating');
  else if (record.status === 'failed') summary = t('pages.xray.schedule.geoUpdateFailed');
  else if (record.status === 'unconfirmed')
    summary = t(
      record.details === 'geoCoreStopped'
        ? 'pages.xray.schedule.geoCoreStopped'
        : 'pages.xray.schedule.geoResultUnavailable',
    );
  else if (record.status === 'updated') summary = t('pages.xray.schedule.updated');
  else if (record.status === 'skipped' && record.details === 'geoNoDownloads')
    summary = t('pages.xray.schedule.geoNoDownloadsSummary');

  const comparisons = new Map(
    record.hashComparisons?.map((comparison) => [comparison.file, comparison]),
  );
  const selectedComparison = selectedFile ? comparisons.get(selectedFile) : undefined;
  const files = [
    ...new Set([...(record.steps?.map((step) => step.target) ?? []), ...comparisons.keys()]),
  ];
  const errors = [
    ...new Set(
      [
        record.error,
        ...(record.steps
          ?.filter((step) => step.error)
          .map((step) => `${step.target}: ${step.error}`) ?? []),
      ].filter(Boolean),
    ),
  ];

  return (
    <>
      <Space
        orientation="vertical"
        style={{
          width: '100%',
          maxWidth: 'min(480px, calc(100vw - 48px))',
          overflowWrap: 'anywhere',
        }}
      >
        <Typography.Text type={record.status === 'failed' ? 'danger' : undefined}>
          {summary}
        </Typography.Text>
        {files.map((file) => {
          const comparison = comparisons.get(file);
          if (!comparison)
            return (
              <Typography.Text key={file} code>
                {file}
              </Typography.Text>
            );
          return (
            <Space key={file} wrap>
              <Button
                type="link"
                size="small"
                style={{ padding: 0, height: 'auto' }}
                onClick={() => setSelectedFile(file)}
              >
                {file}
              </Button>
              <Tag
                color={
                  comparison.status === 'changed'
                    ? 'blue'
                    : comparison.status === 'unavailable'
                      ? 'warning'
                      : 'default'
                }
              >
                {t(
                  `pages.xray.schedule.hash${comparison.status.charAt(0).toUpperCase()}${comparison.status.slice(1)}`,
                  { defaultValue: comparison.status },
                )}
              </Tag>
            </Space>
          );
        })}
        {errors.length > 0 && (
          <Collapse
            size="small"
            style={{ width: '100%' }}
            items={[
              {
                key: 'error',
                label: t('pages.xray.schedule.geoErrorDetails'),
                children: (
                  <Typography.Paragraph
                    copyable
                    style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', marginBottom: 0 }}
                  >
                    {errors.join('\n\n')}
                  </Typography.Paragraph>
                ),
              },
            ]}
          />
        )}
      </Space>
      <Modal
        open={selectedComparison !== undefined}
        title={`${selectedComparison?.file ?? ''} · ${t('pages.xray.schedule.details')}`}
        onCancel={() => setSelectedFile(null)}
        footer={null}
        destroyOnHidden
        width={640}
      >
        {selectedComparison && (
          <Space orientation="vertical" style={{ width: '100%', overflowWrap: 'anywhere' }}>
            <Typography.Text type="secondary">{t('pages.xray.schedule.hashHint')}</Typography.Text>
            {selectedComparison.status === 'baseline' && (
              <Typography.Text type="secondary">
                {t('pages.xray.schedule.hashBaselineHint')}
              </Typography.Text>
            )}
            {selectedComparison.error && (
              <Typography.Text type="warning">
                {t(`pages.xray.schedule.${selectedComparison.error}`, {
                  defaultValue: selectedComparison.error,
                })}
              </Typography.Text>
            )}
            <Typography.Text type="secondary">
              {t('pages.xray.schedule.hashPrevious')}
            </Typography.Text>
            {selectedComparison.previousHash ? (
              <Typography.Text code copyable>
                {selectedComparison.previousHash}
              </Typography.Text>
            ) : (
              '—'
            )}
            <Typography.Text type="secondary">
              {t('pages.xray.schedule.hashCurrent')}
            </Typography.Text>
            {selectedComparison.currentHash ? (
              <Typography.Text code copyable>
                {selectedComparison.currentHash}
              </Typography.Text>
            ) : (
              '—'
            )}
            {!!selectedComparison.baselineAt && (
              <Typography.Text type="secondary">
                {t('pages.xray.schedule.hashBaselineAt')}:{' '}
                {formatScheduleTime(
                  selectedComparison.baselineAt,
                  selectedComparison.baselineTimezone ?? record.timezone ?? 'UTC',
                  i18n.language,
                )}
              </Typography.Text>
            )}
          </Space>
        )}
      </Modal>
    </>
  );
}
