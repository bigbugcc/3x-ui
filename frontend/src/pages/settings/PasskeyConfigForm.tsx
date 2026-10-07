import { useEffect, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Spin,
  Switch,
  Tag,
  Typography,
  message,
} from 'antd';
import { useTranslation } from 'react-i18next';
import { useServerDraft } from '@/hooks/useServerDraft';
import { passkeyRequest, passkeyRoot as root } from '@/api/passkey';
import { PasskeyValidationSchema } from '@/schemas/passkey';
import type { PasskeyConfig, PasskeyConfigView } from '@/schemas/passkey';

type Props = {
  saved?: PasskeyConfigView;
  configError?: string;
  loading: boolean;
  busy: boolean;
  fields: Record<string, string> | null;
  setFields: (errors: Record<string, string> | null) => void;
  onRetry: () => void;
  onSave: (config: PasskeyConfig, version: number) => void;
};

export default function PasskeyConfigForm({
  saved,
  configError,
  loading,
  busy,
  fields,
  setFields,
  onRetry,
  onSave,
}: Props) {
  const { t } = useTranslation();
  const [messageApi, messageHolder] = message.useMessage();
  const [modal, modalHolder] = Modal.useModal();
  const [checking, setChecking] = useState(false);
  const abort = useRef<AbortController | null>(null);
  useEffect(() => () => abort.current?.abort(), []);
  const { draft: configDraft, setDraft: setConfigDraft } = useServerDraft(
    saved,
    (view) => ({ ...view, config: { ...view.config, origins: [...view.config.origins] } }),
    (left, right) =>
      left.version === right.version &&
      JSON.stringify(left.config) === JSON.stringify(right.config),
  );
  const draft = configDraft?.config;
  function payload(): PasskeyConfig | null {
    return draft
      ? {
          ...draft,
          origins: draft.origins.map((line) => line.trim()).filter(Boolean),
        }
      : null;
  }
  const current = payload();
  const dirty = !!(saved && current && JSON.stringify(current) !== JSON.stringify(saved.config));
  function patch(changes: Partial<PasskeyConfig>) {
    setConfigDraft(
      (previous) => previous && { ...previous, config: { ...previous.config, ...changes } },
    );
    setFields({});
  }
  async function validate(): Promise<PasskeyConfig | null> {
    const config = payload();
    if (!config || abort.current || busy) return null;
    const controller = new AbortController();
    abort.current = controller;
    setChecking(true);
    try {
      const result = await passkeyRequest(
        'POST',
        root + '/config/validate',
        PasskeyValidationSchema,
        { config },
        controller.signal,
      );
      if (controller.signal.aborted) return null;
      setFields(result.errors);
      if (Object.keys(result.errors).length) {
        messageApi.error(t('passkey.errors.input'));
        return null;
      }
      if (!result.currentOriginAllowed && result.config.enabled)
        messageApi.warning(t('passkey.currentOriginExcluded'));
      else messageApi.success(t('passkey.configChecked'));
      return result.config;
    } catch (error) {
      if (!controller.signal.aborted)
        messageApi.error(error instanceof Error ? error.message : t('passkey.errors.input'));
      return null;
    } finally {
      abort.current = null;
      if (!controller.signal.aborted) setChecking(false);
    }
  }
  function field(key: string) {
    const errors = fields ?? saved?.validationErrors ?? {};
    return {
      validateStatus: errors[key] ? ('error' as const) : undefined,
      help: errors[key] ? t(errors[key]) : undefined,
    };
  }
  function fillAddress() {
    if (!draft) return;
    if (
      window.location.protocol !== 'https:' ||
      /^\d+(\.\d+){3}$/.test(window.location.hostname) ||
      window.location.hostname.includes(':')
    ) {
      messageApi.warning(t('passkey.browserUnavailable'));
      return;
    }
    modal.confirm({
      title: t('passkey.fillCurrent'),
      content: window.location.origin,
      onOk: () => {
        patch({ rpId: window.location.hostname, origins: [window.location.origin] });
      },
    });
  }

  return (
    <>
      {messageHolder}
      {modalHolder}
      <Card title={t('passkey.configuration')}>
        {configError && (
          <Alert
            type="error"
            title={configError}
            action={<Button onClick={() => onRetry()}>{t('passkey.retry')}</Button>}
          />
        )}
        <Spin spinning={loading}>
          {draft && saved && (
            <>
              <Space wrap style={{ marginBottom: 16 }}>
                <Tag
                  color={
                    Object.keys(saved.validationErrors).length
                      ? 'error'
                      : saved.config.enabled
                        ? 'green'
                        : undefined
                  }
                >
                  {t(
                    Object.keys(saved.validationErrors).length
                      ? 'passkey.errors.input'
                      : saved.config.enabled
                        ? 'passkey.enabled'
                        : 'passkey.disabled',
                  )}
                </Tag>
                <Typography.Text type="secondary">{t('passkey.deploymentHint')}</Typography.Text>
              </Space>
              <Form layout="vertical" disabled={busy || checking}>
                <Form.Item label={t('passkey.enable')} htmlFor="passkey-enable">
                  <Switch
                    id="passkey-enable"
                    checked={draft.enabled}
                    onChange={(enabled) => patch({ enabled })}
                  />
                </Form.Item>
                <Form.Item
                  label={t('passkey.rpId')}
                  htmlFor="passkey-domain"
                  extra={t('passkey.rpIdHint')}
                  {...field('rpId')}
                >
                  <Input
                    id="passkey-domain"
                    value={draft.rpId}
                    placeholder="panel.example.com"
                    onChange={(event) => patch({ rpId: event.target.value })}
                  />
                </Form.Item>
                <Form.Item
                  label={t('passkey.origins')}
                  htmlFor="passkey-origins"
                  extra={t('passkey.originsHint')}
                  {...field('origins')}
                >
                  <Input.TextArea
                    id="passkey-origins"
                    value={draft.origins.join('\n')}
                    autoSize={{ minRows: 2, maxRows: 8 }}
                    placeholder="https://panel.example.com:8443"
                    onChange={(event) => {
                      patch({ origins: event.target.value.split('\n') });
                    }}
                  />
                </Form.Item>
                <Form.Item
                  label={t('passkey.httpsMode')}
                  htmlFor="passkey-mode"
                  {...field('httpsMode')}
                >
                  <Select
                    id="passkey-mode"
                    value={draft.httpsMode}
                    options={[
                      { value: 'direct', label: t('passkey.directHTTPS') },
                      { value: 'proxy', label: t('passkey.proxyHTTPS') },
                    ]}
                    onChange={(httpsMode) => patch({ httpsMode })}
                  />
                </Form.Item>
                <Form.Item
                  label={t('passkey.proxies')}
                  htmlFor="passkey-proxies"
                  extra={t('passkey.proxiesHint')}
                  {...field('trustedProxyCIDRs')}
                >
                  <Input
                    id="passkey-proxies"
                    value={draft.trustedProxyCIDRs}
                    onChange={(event) => patch({ trustedProxyCIDRs: event.target.value })}
                    placeholder="127.0.0.1/32,::1/128"
                  />
                </Form.Item>
                <Alert type="info" title={t('passkey.policyHint')} style={{ marginBottom: 16 }} />
                <Space wrap>
                  <Button onClick={fillAddress}>{t('passkey.fillCurrent')}</Button>
                  <Button loading={checking} onClick={() => void validate()}>
                    {t('passkey.checkConfig')}
                  </Button>
                  <Button
                    type="primary"
                    disabled={!dirty || checking || busy}
                    onClick={async () => {
                      const version = configDraft?.version;
                      const config = await validate();
                      if (config && version !== undefined) onSave(config, version);
                    }}
                  >
                    {t('passkey.saveConfig')}
                  </Button>
                  <Button
                    disabled={!dirty}
                    onClick={() => {
                      setConfigDraft(saved);
                      setFields(saved.validationErrors);
                    }}
                  >
                    {t('passkey.discard')}
                  </Button>
                </Space>
              </Form>
            </>
          )}
        </Spin>
      </Card>
    </>
  );
}
