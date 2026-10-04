import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { FormProvider, useWatch } from 'react-hook-form';
import { FormField, useZodForm } from '@/components/form/rhf';
import { XrayRestartScheduleSchema } from '@/generated/zod';
import {
  Alert,
  Button,
  Form,
  Modal,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';

import { useXrayRestartSchedule } from '@/api/queries/useXraySchedule';
import type { XrayRestartRun } from '@/generated/types';
import ScheduleEditor, { formatScheduleTime } from './ScheduleEditor';

export default function RestartScheduleSection() {
  const { t } = useTranslation();
  const [modal, modalContext] = Modal.useModal();
  const [messageApi, messageContext] = message.useMessage();
  const methods = useZodForm(XrayRestartScheduleSchema, {
    defaultValues: { enabled: false, cron: '0 4 * * 0', timezone: 'UTC' },
  });
  const { reset, setValue } = methods;
  const config = XrayRestartScheduleSchema.parse(useWatch({ control: methods.control }));
  const { query, save: saveMutation, run: runMutation } = useXrayRestartSchedule();
  const initialized = useRef(false);
  const { isDirty } = methods.formState;
  const view = query.data;
  const loaded = view !== undefined;
  const error = query.error?.message ?? '';
  const busy = saveMutation.isPending || runMutation.isPending;
  useEffect(() => {
    if (!view || (initialized.current && isDirty)) return;
    reset(view.config);
    initialized.current = true;
  }, [view, reset, isDirty]);

  async function save() {
    if (!loaded) return;
    try {
      const next = await saveMutation.mutateAsync(config);
      reset(next.config);
      messageApi.success(t('pages.xray.schedule.saved'));
    } catch (error) {
      messageApi.error((error as Error).message);
    }
  }

  function runNow() {
    modal.confirm({
      title: t('pages.xray.schedule.runNow'),
      content: t('pages.xray.schedule.restartHint'),
      onOk: async () => {
        try {
          const result = await runMutation.mutateAsync();
          if (result.status === 'skipped') messageApi.info(t('pages.xray.schedule.skippedHint'));
          else messageApi.success(t('pages.xray.schedule.restartSuccess'));
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
      <Alert
        type="warning"
        showIcon
        title={t('pages.xray.schedule.restartHint')}
        className="mb-12"
      />
      {error && (
        <Alert
          type="error"
          showIcon
          title={error}
          action={<Button onClick={() => void query.refetch()}>{t('check')}</Button>}
        />
      )}
      <Spin spinning={!loaded && !error}>
        {loaded && (
          <FormProvider {...methods}>
            <Form layout="vertical">
              <FormField
                name="enabled"
                valueProp="checked"
                label={t('pages.xray.schedule.enableRestart')}
              >
                <Switch aria-label={t('pages.xray.schedule.enableRestart')} disabled={busy} />
              </FormField>
              <ScheduleEditor
                cron={config.cron}
                timezone={config.timezone}
                allowInterval
                disabled={busy}
                onChange={(patch) => {
                  if (patch.cron !== undefined) setValue('cron', patch.cron, { shouldDirty: true });
                  if (patch.timezone !== undefined)
                    setValue('timezone', patch.timezone, { shouldDirty: true });
                }}
              />
              <Typography.Paragraph>
                {t('pages.xray.schedule.nextSaved')}:{' '}
                {formatScheduleTime(view?.nextRun ?? 0, view?.config.timezone ?? 'UTC')}
              </Typography.Paragraph>
              <Typography.Paragraph type="secondary">
                {t('pages.xray.schedule.manualStopHint')}
              </Typography.Paragraph>
              <Space wrap>
                <Button
                  type="primary"
                  loading={busy}
                  onClick={() => void methods.handleSubmit(save)()}
                >
                  {t('pages.xray.schedule.saveRestart')}
                </Button>
                <Button disabled={busy || view?.running} onClick={runNow}>
                  {t('pages.xray.schedule.runNow')}
                </Button>
              </Space>
            </Form>
          </FormProvider>
        )}
      </Spin>
      <Typography.Title level={5}>{t('pages.xray.schedule.history')}</Typography.Title>
      <Table<XrayRestartRun>
        size="small"
        scroll={{ x: 600 }}
        dataSource={view?.history ?? []}
        rowKey={(record) => `${record.startedAt}-${record.trigger}`}
        pagination={{ pageSize: 10 }}
        columns={[
          {
            title: t('pages.xray.schedule.startedAt'),
            dataIndex: 'startedAt',
            render: (value: number) => formatScheduleTime(value, view?.config.timezone ?? 'UTC'),
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
                color={value === 'success' ? 'success' : value === 'failed' ? 'error' : 'default'}
              >
                {t(`pages.xray.schedule.${value}`)}
              </Tag>
            ),
          },
          {
            title: t('pages.xray.schedule.duration'),
            dataIndex: 'durationMs',
            render: (value: number) => `${value} ms`,
          },
          {
            title: t('pages.xray.schedule.details'),
            render: (_: unknown, record: XrayRestartRun) =>
              record.status === 'skipped'
                ? t('pages.xray.schedule.skippedHint')
                : record.error || '—',
          },
        ]}
      />
    </div>
  );
}
