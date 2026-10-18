import { useState } from 'react';
import { Form } from 'antd';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import ScheduleEditor, { formatScheduleTime } from '@/pages/xray/schedule/ScheduleEditor';
import { renderWithProviders as render } from './test-utils';

function Editor({ initial, disabled = false }: { initial: string; disabled?: boolean }) {
  const [cron, setCron] = useState(initial);
  return (
    <Form>
      <ScheduleEditor
        cron={cron}
        timezone="UTC"
        disabled={disabled}
        onChange={(patch) => {
          if (patch.cron) setCron(patch.cron);
        }}
      />
      <output aria-label="Cron">{cron}</output>
    </Form>
  );
}

describe('schedule time editor', () => {
  it('places the time zone before the frequency and time controls', () => {
    const { container } = render(<Editor initial="17 4 * * 2" />);
    const labels = Array.from(container.querySelectorAll('.ant-form-item-label label')).map(
      (label) => label.textContent,
    );
    expect(labels).toEqual(['Time zone', 'Frequency', 'Execution time', 'Weekday']);
  });
  it.each([
    ['17 4 * * *', '17 23 * * *', '59 23 * * *'],
    ['17 4 * * 2', '17 23 * * 2', '59 23 * * 2'],
  ])(
    'commits typed hours and minutes without selecting an option: %s',
    async (initial, afterHour, afterMinute) => {
      render(<Editor initial={initial} />);
      const user = userEvent.setup();
      const hour = screen.getByRole('spinbutton', { name: 'Hour' });
      const minute = screen.getByRole('spinbutton', { name: 'Minute' });
      await user.clear(hour);
      await user.type(hour, '23');
      expect(screen.getByLabelText('Cron').textContent).toBe(afterHour);
      await user.clear(minute);
      await user.type(minute, '59');
      expect(screen.getByLabelText('Cron').textContent).toBe(afterMinute);
      expect(screen.queryByRole('listbox')).toBeNull();
    },
  );
  it('disables both time controls while saving', () => {
    render(<Editor initial="0 0 * * *" disabled />);
    expect((screen.getByRole('spinbutton', { name: 'Hour' }) as HTMLInputElement).disabled).toBe(
      true,
    );
    expect((screen.getByRole('spinbutton', { name: 'Minute' }) as HTMLInputElement).disabled).toBe(
      true,
    );
  });
  it.each(['Enter', 'Tab', 'blur'])(
    'confirms direct input and pads a single digit on %s',
    async (action) => {
      render(<Editor initial="17 4 * * 2" />);
      const user = userEvent.setup();
      const hour = screen.getByRole('spinbutton', { name: 'Hour' }) as HTMLInputElement;
      await user.clear(hour);
      await user.type(hour, '9');
      if (action === 'blur') await user.click(screen.getByText('Execution time', { exact: true }));
      else await user.keyboard(`{${action}}`);
      expect(hour.value).toBe('09');
      expect(screen.getByLabelText('Cron').textContent).toBe('17 9 * * 2');
    },
  );
  it.each([
    ['Hour', '24', '23', '17 23 * * 2'],
    ['Hour', '-1', '00', '17 0 * * 2'],
    ['Minute', '60', '59', '59 4 * * 2'],
    ['Minute', '-1', '00', '0 4 * * 2'],
  ])('keeps %s within its range when confirming %s', async (name, input, displayed, cron) => {
    render(<Editor initial="17 4 * * 2" />);
    const user = userEvent.setup();
    const field = screen.getByRole('spinbutton', { name }) as HTMLInputElement;
    await user.clear(field);
    await user.type(field, input);
    await user.tab();
    expect(field.value).toBe(displayed);
    expect(screen.getByLabelText('Cron').textContent).toBe(cron);
  });
  it('restores an empty field and never writes a fractional Cron value', async () => {
    render(<Editor initial="17 4 * * 2" />);
    const user = userEvent.setup();
    const hour = screen.getByRole('spinbutton', { name: 'Hour' }) as HTMLInputElement;
    await user.clear(hour);
    expect(screen.getByLabelText('Cron').textContent).toBe('17 4 * * 2');
    await user.tab();
    expect(hour.value).toBe('04');
    await user.clear(hour);
    await user.type(hour, '9.5');
    expect(screen.getByLabelText('Cron').textContent).toBe('17 9 * * 2');
    await user.tab();
    expect(screen.getByLabelText('Cron').textContent).toBe('17 10 * * 2');
  });
  it.each(['zh-CN', 'en-US', 'ja-JP'])(
    'formats the task zone using the panel language: %s',
    (language) => {
      const timestamp = Date.UTC(2026, 9, 17, 16, 15);
      expect(formatScheduleTime(timestamp, 'Asia/Shanghai', language)).toBe(
        `${new Intl.DateTimeFormat(language, { dateStyle: 'medium', timeStyle: 'medium', timeZone: 'Asia/Shanghai' }).format(timestamp)} (Asia/Shanghai)`,
      );
    },
  );
});
