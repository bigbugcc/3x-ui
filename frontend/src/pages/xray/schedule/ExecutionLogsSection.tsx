import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { FormProvider } from 'react-hook-form';
import {
  Alert,
  Button,
  Form,
  InputNumber,
  Modal,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import { FormField, useZodForm } from '@/components/form/rhf';
import { TaskLogSettingsSchema } from '@/generated/zod';
import type { TaskExecutionLog } from '@/generated/types';
import { useTaskExecutionLogs } from '@/api/queries/useXraySchedule';
import { formatScheduleTime } from './ScheduleEditor';
import GeoExecutionDetails from './GeoExecutionDetails';

export default function ExecutionLogsSection() {
  const { t, i18n } = useTranslation();
  const { query, save, clear } = useTaskExecutionLogs();
  const [modal, modalContext] = Modal.useModal();
  const [messageApi, messageContext] = message.useMessage();
  const methods = useZodForm(TaskLogSettingsSchema, { defaultValues: { retentionDays: 30 } });
  const {
    reset,
    formState: { isDirty },
  } = methods;
  const initialized = useRef(false);
  useEffect(() => {
    if (!query.data || (initialized.current && isDirty)) return;
    reset(query.data.settings);
    initialized.current = true;
  }, [query.data, reset, isDirty]);
  const busy = save.isPending || clear.isPending;
  async function saveSettings() {
    try {
      const next = await save.mutateAsync(methods.getValues());
      reset(next.settings);
      messageApi.success(t('pages.xray.schedule.logSettingsSaved'));
    } catch (error) {
      messageApi.error((error as Error).message);
    }
  }
  function clearLogs() {
    modal.confirm({
      title: t('pages.xray.schedule.clearLogs'),
      content: t('pages.xray.schedule.clearLogsConfirm'),
      onOk: async () => {
        try {
          await clear.mutateAsync();
          messageApi.success(t('pages.xray.schedule.logsCleared'));
        } catch (error) {
          messageApi.error((error as Error).message);
        }
      },
    });
  }
  return (
    <div>
      {modalContext}
      {messageContext}
      {query.error && <Alert type="error" showIcon title={query.error.message} className="mb-12" />}
      <FormProvider {...methods}>
        <Form
          layout="inline"
          className="mb-12"
          onFinish={() => void methods.handleSubmit(saveSettings)()}
        >
          <FormField
            name="retentionDays"
            label={t('pages.xray.schedule.retentionDays')}
            rules={{
              validate: (value: number) =>
                (Number.isInteger(value) && value >= 1 && value <= 365) ||
                'pages.xray.schedule.retentionRange',
            }}
          >
            <InputNumber min={1} max={365} precision={0} disabled={busy || !query.data} />
          </FormField>
          <Space wrap>
            <Button
              type="primary"
              htmlType="submit"
              disabled={busy || !query.data}
              loading={save.isPending}
            >
              {t('pages.xray.schedule.saveLogSettings')}
            </Button>
            <Button danger onClick={clearLogs} disabled={busy || !query.data}>
              {t('pages.xray.schedule.clearLogs')}
            </Button>
            <Button onClick={() => void query.refetch()} disabled={busy} loading={query.isFetching}>
              {t('refresh')}
            </Button>
          </Space>
        </Form>
      </FormProvider>
      <Typography.Paragraph type="secondary">
        {t('pages.xray.schedule.logRetentionHint')}
      </Typography.Paragraph>
      <Typography.Title level={5}>{t('pages.xray.schedule.history')}</Typography.Title>
      <Table<TaskExecutionLog>
        size="small"
        loading={query.isPending}
        scroll={{ x: 800 }}
        dataSource={query.data?.history ?? []}
        rowKey="id"
        pagination={{ pageSize: 10 }}
        columns={[
          {
            title: t('pages.xray.schedule.task'),
            dataIndex: 'task',
            filters: [
              { text: t('pages.xray.schedule.restartTitle'), value: 'restart' },
              { text: t('pages.xray.schedule.geoTitle'), value: 'geodata' },
            ],
            onFilter: (value, record) => record.task === value,
            render: (value: string) =>
              t(
                value === 'restart'
                  ? 'pages.xray.schedule.restartTitle'
                  : 'pages.xray.schedule.geoTitle',
              ),
          },
          {
            title: t('pages.xray.schedule.startedAt'),
            dataIndex: 'startedAt',
            render: (value: number, record) => (
              <Space orientation="vertical" size={0}>
                <span>{formatScheduleTime(value, record.timezone ?? 'UTC', i18n.language)}</span>
                {record.timezoneInferred && (
                  <Typography.Text type="secondary">
                    {t('pages.xray.schedule.logTimezoneInferred')}
                  </Typography.Text>
                )}
              </Space>
            ),
          },
          {
            title: t('pages.xray.schedule.trigger'),
            dataIndex: 'trigger',
            render: (value: string) => t(`pages.xray.schedule.${value}`),
          },
          {
            title: t('pages.xray.schedule.result'),
            dataIndex: 'status',
            render: (value: string) => (
              <Tag
                color={
                  value === 'success' || value === 'updated'
                    ? 'success'
                    : value === 'failed'
                      ? 'error'
                      : 'default'
                }
              >
                {t(`pages.xray.schedule.${value}`)}
              </Tag>
            ),
          },
          {
            title: t('pages.xray.schedule.duration'),
            dataIndex: 'durationMs',
            render: (value: number, record) =>
              record.status === 'running' ||
              record.status === 'unconfirmed' ||
              record.status === 'updated'
                ? '—'
                : `${value} ms`,
          },
          {
            title: t('pages.xray.schedule.details'),
            render: (_: unknown, record) =>
              record.task === 'geodata' ? (
                <GeoExecutionDetails record={record} />
              ) : (
                <Space orientation="vertical">
                  {record.status === 'skipped'
                    ? t('pages.xray.schedule.skippedHint')
                    : record.error ||
                      (record.details
                        ? t(`pages.xray.schedule.${record.details}`, {
                            defaultValue: record.details,
                          })
                        : '—')}
                  {record.steps?.map((step) => (
                    <span key={step.target}>
                      {step.target}: {t(`pages.xray.schedule.${step.status}`)}
                      {step.error && ` · ${step.error}`}
                    </span>
                  ))}
                </Space>
              ),
          },
        ]}
      />
    </div>
  );
}
