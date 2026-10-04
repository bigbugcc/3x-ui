import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { FormProvider, useWatch } from 'react-hook-form';
import { FormField, useZodForm } from '@/components/form/rhf';
import { GeodataUpdateScheduleSchema } from '@/generated/zod';
import {
  Alert,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Spin,
  Switch,
  Typography,
  message,
} from 'antd';
import { PlusOutlined, DeleteOutlined } from '@ant-design/icons';

import { useGeodataUpdateSchedule } from '@/api/queries/useXraySchedule';
import type { GeodataUpdateSchedule } from '@/generated/types';
import ScheduleEditor, { formatScheduleTime } from '@/pages/xray/schedule/ScheduleEditor';

interface Props {
  active: boolean;
  onBusy?: (e: { busy: boolean; tip?: string }) => void;
  onClose?: () => void;
  saveDisabled?: boolean;
}

const FILE_NAME_PATTERN = /^[A-Za-z0-9._-]+\.dat$/;

export default function GeodataSection({ active, onBusy, onClose, saveDisabled }: Props) {
  const { t } = useTranslation();
  const [modal, modalContextHolder] = Modal.useModal();
  const [messageApi, messageContextHolder] = message.useMessage();
  const methods = useZodForm(GeodataUpdateScheduleSchema, {
    defaultValues: { enabled: false, cron: '0 4 * * *', timezone: 'UTC', outbound: '', assets: [] },
  });
  const { reset } = methods;
  const config = GeodataUpdateScheduleSchema.parse(useWatch({ control: methods.control }));
  const { query, save: saveMutation } = useGeodataUpdateSchedule(active);
  const initialized = useRef(false);
  const { isDirty } = methods.formState;
  const view = query.data;
  const loaded = view !== undefined;
  const error = query.error?.message ?? '';
  const busy = saveMutation.isPending;
  function setConfig(next: GeodataUpdateSchedule) {
    methods.setValue('enabled', next.enabled, { shouldDirty: true });
    methods.setValue('cron', next.cron, { shouldDirty: true });
    methods.setValue('timezone', next.timezone, { shouldDirty: true });
    methods.setValue('outbound', next.outbound, { shouldDirty: true });
    methods.setValue('assets', next.assets, { shouldDirty: true });
  }
  useEffect(() => {
    if (!active) {
      initialized.current = false;
      return;
    }
    if (!view || (initialized.current && isDirty)) return;
    reset(view.config);
    initialized.current = true;
  }, [active, view, reset, isDirty]);

  function setRow(index: number, patch: { file?: string; url?: string }) {
    setConfig({
      ...config,
      assets: config.assets.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    });
  }

  function save() {
    if (!loaded || saveDisabled) return;
    const plan = {
      ...config,
      cron: config.cron.trim(),
      timezone: config.timezone.trim(),
      assets: config.assets
        .map((row) => ({ url: row.url.trim(), file: row.file.trim() }))
        .filter((row) => row.url || row.file),
    };
    for (const asset of plan.assets) {
      try {
        const url = new URL(asset.url);
        if (url.protocol !== 'https:' || !url.hostname || url.username || url.password)
          throw new Error();
      } catch {
        messageApi.error(t('pages.index.geodataInvalidUrl'));
        return;
      }
      if (!FILE_NAME_PATTERN.test(asset.file) || asset.file.includes('..')) {
        messageApi.error(t('pages.index.geodataInvalidFile'));
        return;
      }
    }
    modal.confirm({
      title: t('pages.index.geodataConfirmTitle'),
      content: t('pages.xray.schedule.geoSaveHint'),
      okText: t('confirm'),
      cancelText: t('cancel'),
      onOk: async () => {
        onBusy?.({ busy: true, tip: t('pages.index.dontRefresh') });
        try {
          const next = await saveMutation.mutateAsync(plan);
          reset(next.config);
          if (next.applyError)
            messageApi.warning(t('pages.xray.schedule.applyFailed', { error: next.applyError }));
          else if (!next.applied) messageApi.info(t('pages.xray.schedule.applyOnStart'));
          else messageApi.success(t('pages.xray.schedule.saved'));
          if (!next.applyError) onClose?.();
        } catch (error) {
          messageApi.error((error as Error).message);
        } finally {
          onBusy?.({ busy: false });
        }
      },
    });
  }

  return (
    <div>
      {modalContextHolder}
      {messageContextHolder}
      <Alert type="info" className="mb-12" title={t('pages.index.geodataHint')} showIcon />
      <Typography.Paragraph type="secondary">
        {t('pages.xray.schedule.geoHistoryHint')}
      </Typography.Paragraph>
      {error && (
        <Alert
          type="error"
          title={error}
          showIcon
          action={<Button onClick={() => void query.refetch()}>{t('check')}</Button>}
        />
      )}
      {view?.applyError && (
        <Alert
          type="warning"
          title={t('pages.xray.schedule.applyFailed', { error: view.applyError })}
          showIcon
        />
      )}
      <Spin spinning={!loaded && !error}>
        {loaded && (
          <FormProvider {...methods}>
            <Form layout="vertical">
              <FormField
                name="enabled"
                valueProp="checked"
                label={t('pages.xray.schedule.enableGeo')}
              >
                <Switch aria-label={t('pages.xray.schedule.enableGeo')} disabled={busy} />
              </FormField>
              <ScheduleEditor
                cron={config.cron}
                timezone={config.timezone}
                disabled={busy}
                onChange={(patch) => setConfig({ ...config, ...patch })}
              />
              <FormField
                name="outbound"
                transform={{ input: (value) => value || undefined, output: (value) => value ?? '' }}
                label={t('pages.index.geodataOutbound')}
              >
                <Select
                  style={{ width: '100%' }}
                  allowClear
                  disabled={busy}
                  options={view?.outboundTags.map((tag) => ({ label: tag, value: tag }))}
                />
              </FormField>
            </Form>
            <Typography.Paragraph>
              {t('pages.xray.schedule.nextSaved')}:{' '}
              {formatScheduleTime(view?.nextRun ?? 0, view?.config.timezone ?? 'UTC')}
            </Typography.Paragraph>
            <Space orientation="vertical" style={{ width: '100%' }} size={8}>
              {config.assets.length === 0 && (
                <Typography.Text type="secondary">{t('pages.index.geodataEmpty')}</Typography.Text>
              )}
              {config.assets.map((row, index) => (
                <Space.Compact key={index} style={{ width: '100%' }}>
                  <Input
                    aria-label={`${t('pages.xray.schedule.downloadUrl')} ${index + 1}`}
                    style={{ width: '60%' }}
                    placeholder="https://example.com/geosite_custom.dat"
                    value={row.url}
                    disabled={busy}
                    onChange={(event) => setRow(index, { url: event.target.value })}
                    onBlur={() => {
                      if (row.file) return;
                      try {
                        const filename = new URL(row.url).pathname.split('/').pop() ?? '';
                        if (FILE_NAME_PATTERN.test(filename)) setRow(index, { file: filename });
                      } catch {
                        /* Wait until a complete URL has been entered. */
                      }
                    }}
                  />
                  <Input
                    aria-label={`${t('pages.index.geodataFile')} ${index + 1}`}
                    style={{ width: '40%' }}
                    placeholder={t('pages.index.geodataFile')}
                    value={row.file}
                    disabled={busy}
                    onChange={(event) => setRow(index, { file: event.target.value })}
                  />
                  <Button
                    aria-label={t('delete')}
                    icon={<DeleteOutlined />}
                    disabled={busy}
                    onClick={() =>
                      setConfig({ ...config, assets: config.assets.filter((_, i) => i !== index) })
                    }
                  />
                </Space.Compact>
              ))}
              <Space wrap>
                <Button
                  icon={<PlusOutlined />}
                  disabled={busy}
                  onClick={() =>
                    setConfig({ ...config, assets: [...config.assets, { url: '', file: '' }] })
                  }
                >
                  {t('pages.index.geodataAddFile')}
                </Button>
                <Button
                  disabled={busy || !view?.standardSources.length}
                  onClick={() => {
                    const files = new Set(config.assets.map((row) => row.file));
                    setConfig({
                      ...config,
                      assets: [
                        ...config.assets,
                        ...(view?.standardSources ?? []).filter(
                          (source) => !files.has(source.file),
                        ),
                      ],
                    });
                  }}
                >
                  {t('pages.index.geodataUseStandardSources')}
                </Button>
                <Button
                  type="primary"
                  loading={busy}
                  disabled={saveDisabled}
                  onClick={() => void methods.handleSubmit(save)()}
                >
                  {t('pages.xray.schedule.saveGeo')}
                </Button>
              </Space>
            </Space>
          </FormProvider>
        )}
      </Spin>
    </div>
  );
}
