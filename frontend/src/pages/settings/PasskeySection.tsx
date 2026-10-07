import PasskeyConfigForm from './PasskeyConfigForm';
import PasskeyCredentials from './PasskeyCredentials';
import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Form, Input, Modal, Space, Typography, message } from 'antd';
import type { InputRef } from 'antd';
import { useTranslation } from 'react-i18next';
import { creationOptions, passkeySupported, serializeCredential } from '@/utils/passkey';
import { passkeyRequest, PasskeyRequestError, passkeyRoot as root } from '@/api/passkey';
import { usePasskeys } from '@/api/queries/usePasskeys';
import {
  PasskeyAuthorizationSchema,
  PasskeyEmptyResultSchema,
  PasskeyRegistrationSchema,
  PasskeySessionResultSchema,
  PasskeyNameSchema,
} from '@/schemas/passkey';
import type { PasskeyConfig, PasskeyRow as Row } from '@/schemas/passkey';
type Action =
  | { kind: 'save'; config: PasskeyConfig; version: number }
  | { kind: 'add' }
  | { kind: 'delete'; row: Row };

export default function PasskeySection() {
  const { t } = useTranslation();
  const [messageApi, messageHolder] = message.useMessage();
  const {
    config: configQuery,
    credentials,
    rename: renameMutation,
    invalidateCredentials,
  } = usePasskeys();
  const saved = configQuery.data;
  const configError = configQuery.error?.message;
  const listError = credentials.error?.message;
  const rows = credentials.data ?? [];
  const loading = configQuery.isFetching;
  const listLoading = credentials.isFetching;
  const [fields, setFields] = useState<Record<string, string> | null>(null);
  const [action, setAction] = useState<Action | null>(null);
  const [busy, setBusy] = useState(false);
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [rename, setRename] = useState<Row | null>(null);
  const abort = useRef<AbortController | null>(null);
  const passwordRef = useRef<InputRef>(null);
  useEffect(() => () => abort.current?.abort(), []);
  function cancel() {
    abort.current?.abort();
    setAction(null);
    setPassword('');
    setCode('');
    setName('');
  }
  function open(next: Action) {
    if (abort.current || busy || rename) return;
    setPassword('');
    setCode('');
    setName('');
    setAction(next);
  }
  function exit() {
    window.location.replace(window.X_UI_BASE_PATH || '/');
  }

  async function submit() {
    if (
      !action ||
      !saved ||
      abort.current ||
      busy ||
      !password ||
      (saved.twoFactorEnabled && !code.trim()) ||
      (action.kind === 'add' && !PasskeyNameSchema.safeParse(name).success)
    )
      return;
    setBusy(true);
    const controller = new AbortController();
    abort.current = controller;
    try {
      if (action.kind === 'save') {
        const result = await passkeyRequest(
          'POST',
          root + '/config',
          PasskeySessionResultSchema,
          {
            config: action.config,
            expectedVersion: action.version,
            currentPassword: password,
            twoFactorCode: code,
          },
          controller.signal,
        );
        if (result.reauthRequired) exit();
      } else {
        const authorization = await passkeyRequest(
          'POST',
          root + '/reauth',
          PasskeyAuthorizationSchema,
          {
            purpose: action.kind === 'add' ? 'register' : 'delete',
            targetId: action.kind === 'delete' ? action.row.id : 0,
            currentPassword: password,
            twoFactorCode: code,
          },
          controller.signal,
        );
        if (action.kind === 'delete') {
          await passkeyRequest(
            'POST',
            root + '/delete/' + action.row.id,
            PasskeySessionResultSchema,
            authorization,
            controller.signal,
          );
          exit();
        } else {
          const begin = await passkeyRequest(
            'POST',
            root + '/register/begin',
            PasskeyRegistrationSchema,
            { name, authorizationId: authorization.authorizationId },
            controller.signal,
          );
          const credential = (await navigator.credentials.create({
            publicKey: creationOptions(begin.publicKey),
            signal: controller.signal,
          })) as PublicKeyCredential | null;
          if (!credential) throw new DOMException('', 'NotAllowedError');
          try {
            await passkeyRequest(
              'POST',
              root + '/register/finish',
              PasskeyEmptyResultSchema,
              { ceremonyId: begin.ceremonyId, credential: serializeCredential(credential) },
              controller.signal,
            );
          } catch (error) {
            if (!controller.signal.aborted) messageApi.warning(t('passkey.bindingIncomplete'));
            throw error;
          }
          messageApi.success(t('passkey.added'));
          setAction(null);
          setName('');
          await invalidateCredentials();
        }
      }
    } catch (error) {
      if (!controller.signal.aborted) {
        if (error instanceof PasskeyRequestError) setFields(error.fields);
        messageApi.error(
          error instanceof DOMException
            ? t('passkey.notCompleted')
            : error instanceof Error
              ? error.message
              : t('passkey.errors.save'),
        );
      }
    } finally {
      abort.current = null;
      setBusy(false);
      setPassword('');
      setCode('');
    }
  }
  return (
    <Space orientation="vertical" size="large" style={{ width: '100%' }}>
      {messageHolder}
      <PasskeyConfigForm
        saved={saved}
        configError={configError}
        loading={loading}
        busy={busy || !!action || !!rename}
        fields={fields}
        setFields={setFields}
        onRetry={() => void configQuery.refetch()}
        onSave={(config, version) => open({ kind: 'save', config, version })}
      />
      <PasskeyCredentials
        saved={saved}
        rows={rows}
        loading={listLoading}
        error={listError}
        busy={busy || !!action || !!rename}
        supported={passkeySupported()}
        onAdd={() => open({ kind: 'add' })}
        onRetry={() => void credentials.refetch()}
        onRename={(row) => {
          setRename(row);
          setName(row.name);
        }}
        onDelete={(row) => open({ kind: 'delete', row })}
      />
      <Modal
        open={!!action}
        destroyOnHidden
        title={t(
          action?.kind === 'save'
            ? 'passkey.saveConfig'
            : action?.kind === 'delete'
              ? 'passkey.deleteConfirm'
              : 'passkey.add',
        )}
        confirmLoading={busy}
        onOk={submit}
        onCancel={cancel}
        afterOpenChange={(open) => {
          if (open) passwordRef.current?.focus();
        }}
        okButtonProps={{
          disabled:
            !password ||
            (saved?.twoFactorEnabled && !code.trim()) ||
            (action?.kind === 'add' && !PasskeyNameSchema.safeParse(name).success),
        }}
      >
        <Typography.Paragraph>{t('passkey.reauthHint')}</Typography.Paragraph>
        {action?.kind === 'save' && (
          <>
            <Alert type="warning" title={t('passkey.saveImpact')} />
            <Typography.Paragraph style={{ marginTop: 12 }}>
              {action.config.rpId} · {action.config.origins.join(', ')}
            </Typography.Paragraph>
            {saved && action.config.rpId !== saved.config.rpId && (
              <Alert type="warning" title={t('passkey.rpChange')} />
            )}
            {!action.config.enabled && <Alert type="info" title={t('passkey.disableImpact')} />}
          </>
        )}
        {action?.kind === 'delete' && (
          <Alert type="warning" title={t('passkey.deleteImpact')} description={action.row.name} />
        )}
        <Form layout="vertical" style={{ marginTop: 16 }}>
          {action?.kind === 'add' && (
            <Form.Item label={t('passkey.name')} htmlFor="passkey-name" required>
              <Input
                id="passkey-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
            </Form.Item>
          )}
          <Form.Item
            label={t('pages.settings.currentPassword')}
            htmlFor="passkey-password"
            required
          >
            <Input.Password
              id="passkey-password"
              ref={passwordRef}
              value={password}
              autoComplete="current-password"
              onChange={(event) => setPassword(event.target.value)}
            />
          </Form.Item>
          {saved?.twoFactorEnabled && (
            <Form.Item label={t('twoFactorCode')} htmlFor="passkey-code" required>
              <Input
                id="passkey-code"
                value={code}
                autoComplete="one-time-code"
                onChange={(event) => setCode(event.target.value)}
              />
            </Form.Item>
          )}
        </Form>
        {busy && <Button onClick={cancel}>{t('cancel')}</Button>}
      </Modal>
      <Modal
        open={!!rename}
        title={t('passkey.rename')}
        onCancel={() => {
          setRename(null);
          setName('');
        }}
        confirmLoading={busy}
        okButtonProps={{ disabled: !PasskeyNameSchema.safeParse(name).success }}
        onOk={async () => {
          if (!rename || busy || !PasskeyNameSchema.safeParse(name).success) return;
          setBusy(true);
          try {
            await renameMutation.mutateAsync({ id: rename.id, name });
            setRename(null);
            setName('');
          } catch (error) {
            messageApi.error(error instanceof Error ? error.message : t('passkey.errors.save'));
          } finally {
            setBusy(false);
          }
        }}
      >
        <Input
          aria-label={t('passkey.name')}
          value={name}
          onChange={(event) => setName(event.target.value)}
        />
      </Modal>
    </Space>
  );
}
