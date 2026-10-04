import { AutoComplete, Form, Input, InputNumber, Select, TimePicker, Typography } from 'antd';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';

interface Props {
  cron: string;
  timezone: string;
  allowInterval?: boolean;
  disabled?: boolean;
  onChange: (patch: { cron?: string; timezone?: string }) => void;
}

export function formatScheduleTime(timestamp: number, timezone: string) {
  if (!timestamp) return '—';
  try {
    const formatted = new Intl.DateTimeFormat(undefined, {
      dateStyle: 'medium',
      timeStyle: 'medium',
      timeZone: timezone === 'Local' ? 'UTC' : timezone,
    }).format(timestamp);
    return timezone === 'Local' ? `${formatted} UTC` : formatted;
  } catch {
    return new Date(timestamp).toLocaleString();
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
          <TimePicker
            aria-label={t('pages.xray.schedule.time')}
            format="HH:mm"
            allowClear={false}
            disabled={disabled}
            value={dayjs().hour(hour).minute(minute).second(0)}
            onChange={(value) => {
              if (value) {
                onChange({
                  cron: `${value.minute()} ${value.hour()} * * ${mode === 'weekly' ? weekday : '*'}`,
                });
              }
            }}
          />
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
    </>
  );
}
