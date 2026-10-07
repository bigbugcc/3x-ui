import { useEffect, useRef, useState } from 'react';
import { Button, Space, Typography, message } from 'antd';
import { KeyOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { passkeySupported, requestOptions, serializeCredential } from '@/utils/passkey';
import { passkeyRequest } from '@/api/passkey';
import {
  PasskeyLoginSchema,
  PasskeyStatusSchema,
  PasskeyEmptyResultSchema,
} from '@/schemas/passkey';
import type { PasskeyStatus } from '@/schemas/passkey';

export default function PasskeyLogin({
  passwordBusy,
  onBusyChange,
}: {
  passwordBusy: boolean;
  onBusyChange: (busy: boolean) => void;
}) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<PasskeyStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const controller = useRef<AbortController | null>(null);
  const [messageApi, contextHolder] = message.useMessage();
  useEffect(() => {
    const abort = new AbortController();
    passkeyRequest('GET', '/passkey/status', PasskeyStatusSchema, undefined, abort.signal)
      .then((value) => {
        if (!abort.signal.aborted) setStatus(value);
      })
      .catch(() => {});
    return () => {
      abort.abort();
      controller.current?.abort();
    };
  }, []);
  useEffect(() => {
    if (passwordBusy) controller.current?.abort();
  }, [passwordBusy]);
  async function login() {
    if (controller.current || passwordBusy) return;
    const abort = new AbortController();
    controller.current = abort;
    setBusy(true);
    onBusyChange(true);
    try {
      const begin = await passkeyRequest(
        'POST',
        '/passkey/login/begin',
        PasskeyLoginSchema,
        {},
        abort.signal,
      );
      const credential = (await navigator.credentials.get({
        publicKey: requestOptions(begin.publicKey),
        signal: abort.signal,
      })) as PublicKeyCredential | null;
      if (!credential) throw new DOMException('', 'NotAllowedError');
      await passkeyRequest(
        'POST',
        '/passkey/login/finish',
        PasskeyEmptyResultSchema,
        { ceremonyId: begin.ceremonyId, credential: serializeCredential(credential) },
        abort.signal,
      );
      window.location.href = (window.X_UI_BASE_PATH || '') + 'panel/';
    } catch (error) {
      if (!abort.signal.aborted)
        messageApi.error(
          error instanceof DOMException
            ? t('passkey.notCompleted')
            : error instanceof Error
              ? error.message
              : t('passkey.notCompleted'),
        );
    } finally {
      controller.current = null;
      setBusy(false);
      onBusyChange(false);
    }
  }
  if (!status?.enabled) return null;
  const supported = passkeySupported();
  return (
    <div style={{ marginBottom: 16 }}>
      {contextHolder}
      <Space orientation="vertical" style={{ width: '100%' }}>
        <Button
          block
          size="large"
          htmlType="button"
          icon={<KeyOutlined />}
          loading={busy}
          disabled={passwordBusy || !supported || !status.available}
          onClick={login}
        >
          {t('passkey.login')}
        </Button>
        {busy && (
          <Button block onClick={() => controller.current?.abort()}>
            {t('cancel')}
          </Button>
        )}
        {(!supported || !status.available) && (
          <Typography.Text type="secondary">
            {t(!supported ? 'passkey.browserUnavailable' : 'passkey.siteUnavailable')}
          </Typography.Text>
        )}
      </Space>
    </div>
  );
}
