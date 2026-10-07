import { Alert, Button, Card, Empty, Space, Spin, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import type { PasskeyConfigView, PasskeyRow } from '@/schemas/passkey';

type Props = {
  saved?: PasskeyConfigView;
  rows: PasskeyRow[];
  loading: boolean;
  error?: string;
  busy: boolean;
  supported: boolean;
  onAdd: () => void;
  onRetry: () => void;
  onRename: (row: PasskeyRow) => void;
  onDelete: (row: PasskeyRow) => void;
};

export default function PasskeyCredentials({
  saved,
  rows,
  loading,
  error,
  busy,
  supported,
  onAdd,
  onRetry,
  onRename,
  onDelete,
}: Props) {
  const { t } = useTranslation();
  return (
    <Card
      title={t('passkey.myPasskeys')}
      extra={
        <Button
          type="primary"
          disabled={
            !saved?.config.enabled ||
            !saved.currentOriginAllowed ||
            !supported ||
            busy ||
            loading ||
            !!error ||
            rows.length >= 10
          }
          onClick={onAdd}
        >
          {t('passkey.add')}
        </Button>
      }
    >
      <Typography.Paragraph type="secondary">{t('passkey.credentialsHint')}</Typography.Paragraph>
      {(!supported || !saved?.config.enabled || !saved.currentOriginAllowed) && (
        <Alert
          type="info"
          title={t(!supported ? 'passkey.browserUnavailable' : 'passkey.siteUnavailable')}
          style={{ marginBottom: 12 }}
        />
      )}
      {error && (
        <Alert
          type="error"
          title={error}
          action={<Button onClick={onRetry}>{t('passkey.retry')}</Button>}
        />
      )}
      <Spin spinning={loading}>
        {!error && !rows.length && !loading && <Empty description={t('passkey.empty')} />}
        {rows.map((row) => (
          <Card size="small" key={row.id} style={{ marginBottom: 8 }}>
            <Space orientation="vertical" style={{ width: '100%' }}>
              <Typography.Text strong>{row.name}</Typography.Text>
              <Typography.Text type="secondary">
                {t('passkey.createdAt')}: {new Date(row.createdAt).toLocaleString()} ·{' '}
                {t('passkey.lastUsedAt')}:{' '}
                {row.lastUsedAt
                  ? new Date(row.lastUsedAt).toLocaleString()
                  : t('passkey.neverUsed')}
              </Typography.Text>
              <Typography.Text type="secondary">{row.rpId}</Typography.Text>
              <Space>
                <Button size="small" disabled={busy} onClick={() => onRename(row)}>
                  {t('passkey.rename')}
                </Button>
                <Button size="small" danger disabled={busy} onClick={() => onDelete(row)}>
                  {t('delete')}
                </Button>
              </Space>
            </Space>
          </Card>
        ))}
      </Spin>
    </Card>
  );
}
