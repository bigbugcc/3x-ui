import { AutoComplete, Form, Input, InputNumber, Select, Space, Typography } from 'antd';
import { useTranslation } from 'react-i18next';

function formatTimePart(
  value: number | undefined,
  { userTyping, input }: { userTyping: boolean; input: string },
) {
  return userTyping ? input : String(value ?? 0).padStart(2, '0');
}

interface Props {
  cron: string;
  timezone: string;
  allowInterval?: boolean;
  disabled?: boolean;
  onChange: (patch: { cron?: string; timezone?: string }) => void;
}

export function formatScheduleTime(timestamp: number, timezone: string, language?: string) {
  if (!timestamp) return '—';
  if (timezone === 'Local') return '—';
  try {
    const offset = /^([+-])(\d{2}):(\d{2})$/.exec(timezone);
    const offsetMinutes = offset
      ? (Number(offset[2]) * 60 + Number(offset[3])) * (offset[1] === '-' ? -1 : 1)
      : 0;
    const formatted = new Intl.DateTimeFormat(language, {
      dateStyle: 'medium',
      timeStyle: 'medium',
      timeZone: offset ? 'UTC' : timezone,
    }).format(timestamp + offsetMinutes * 60000);
    return `${formatted} (${offset ? `UTC${timezone}` : timezone})`;
  } catch {
    return `${new Date(timestamp).toISOString()} (UTC)`;
  }
}

export default function ScheduleEditor({
  cron,
  timezone,
  allowInterval = false,
  disabled = false,
  onChange,
}: Props) {
  const { t } = useTranslation();
  const daily = /^(\d+) (\d+) \* \* \*$/.exec(cron);
  const weekly = /^(\d+) (\d+) \* \* ([0-6])$/.exec(cron);
  const interval = /^@every (\d+)(m|h)$/.exec(cron);
  const mode = daily ? 'daily' : weekly ? 'weekly' : interval ? 'interval' : 'custom';
  const hour = Number((daily || weekly)?.[2] ?? 4);
  const minute = Number((daily || weekly)?.[1] ?? 0);
  const weekday = weekly?.[3] ?? '0';
  const options = ['daily', 'weekly', ...(allowInterval ? ['interval'] : []), 'custom'].map(
    (value) => ({ value, label: t(`pages.xray.schedule.${value}`) }),
  );

  return (
    <>
      <Form.Item
        label={t('pages.xray.schedule.timezone')}
        extra={t('pages.xray.schedule.timezoneHint')}
      >
        <AutoComplete
          aria-label={t('pages.xray.schedule.timezone')}
          value={timezone}
          disabled={disabled}
          options={[
            'UTC',
            'Asia/Shanghai',
            'Asia/Tokyo',
            'Europe/London',
            'America/New_York',
            'Local',
          ].map((value) => ({ value }))}
          onChange={(value) => onChange({ timezone: value })}
        />
      </Form.Item>
      <Form.Item label={t('pages.xray.schedule.frequency')}>
        <Select
          aria-label={t('pages.xray.schedule.frequency')}
          value={mode}
          disabled={disabled}
          options={options}
          onChange={(value) =>
            onChange({
              cron:
                value === 'interval'
                  ? '@every 1440m'
                  : value === 'weekly'
                    ? `${minute} ${hour} * * 0`
                    : value === 'daily'
                      ? `${minute} ${hour} * * *`
                      : '0 4 */2 * *',
            })
          }
        />
      </Form.Item>
      {(mode === 'daily' || mode === 'weekly') && (
        <Form.Item label={t('pages.xray.schedule.time')}>
          <Space style={{ direction: 'ltr' }}>
            <InputNumber
              aria-label={t('pages.xray.schedule.hour')}
              inputMode="numeric"
              min={0}
              max={23}
              precision={0}
              formatter={formatTimePart}
              style={{ width: 88 }}
              disabled={disabled}
              value={hour}
              onChange={(value) => {
                if (value != null && Number.isInteger(value)) {
                  onChange({ cron: `${minute} ${value} * * ${mode === 'weekly' ? weekday : '*'}` });
                }
              }}
              onPressEnter={(event) => event.preventDefault()}
            />
            <span aria-hidden>:</span>
            <InputNumber
              aria-label={t('pages.xray.schedule.minute')}
              inputMode="numeric"
              min={0}
              max={59}
              precision={0}
              formatter={formatTimePart}
              style={{ width: 88 }}
              disabled={disabled}
              value={minute}
              onChange={(value) => {
                if (value != null && Number.isInteger(value)) {
                  onChange({ cron: `${value} ${hour} * * ${mode === 'weekly' ? weekday : '*'}` });
                }
              }}
              onPressEnter={(event) => event.preventDefault()}
            />
          </Space>
        </Form.Item>
      )}
      {mode === 'weekly' && (
        <Form.Item label={t('pages.xray.schedule.weekday')}>
          <Select
            aria-label={t('pages.xray.schedule.weekday')}
            value={weekday}
            disabled={disabled}
            options={Array.from({ length: 7 }, (_, i) => ({
              value: String(i),
              label: t(`pages.xray.schedule.day${i}`),
            }))}
            onChange={(value) => onChange({ cron: `${minute} ${hour} * * ${value}` })}
          />
        </Form.Item>
      )}
      {mode === 'interval' && (
        <Form.Item label={t('pages.xray.schedule.intervalMinutes')}>
          <InputNumber
            aria-label={t('pages.xray.schedule.intervalMinutes')}
            min={1}
            max={525600}
            precision={0}
            disabled={disabled}
            value={Number(interval?.[1] ?? 1440) * (interval?.[2] === 'h' ? 60 : 1)}
            onChange={(value) => {
              if (value != null) onChange({ cron: `@every ${value}m` });
            }}
          />
          <Typography.Paragraph type="secondary">
            {t('pages.xray.schedule.intervalHint')}
          </Typography.Paragraph>
        </Form.Item>
      )}
      {mode === 'custom' && (
        <Form.Item label={t('pages.index.geodataCron')} extra={t('pages.xray.schedule.cronHint')}>
          <Input
            aria-label={t('pages.index.geodataCron')}
            value={cron}
            disabled={disabled}
            onChange={(event) => onChange({ cron: event.target.value })}
          />
        </Form.Item>
      )}
    </>
  );
}
