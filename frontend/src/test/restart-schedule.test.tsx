import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import RestartScheduleSection from '@/pages/xray/schedule/RestartScheduleSection';
import { HttpUtil, Msg } from '@/utils';
import { renderWithProviders as render } from './test-utils';

const VIEW = {
  config: { enabled: false, cron: '0 4 * * 0', timezone: 'UTC' },
  nextRun: 0,
  running: false,
  history: [],
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('restart schedule', () => {
  it('preserves an unsaved plan when polling updates the running status', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    vi.spyOn(HttpUtil, 'get')
      .mockResolvedValueOnce(new Msg(true, '', VIEW))
      .mockResolvedValue(new Msg(true, '', { ...VIEW, running: true }));
    const user = userEvent.setup();
    render(<RestartScheduleSection />);
    const toggle = await screen.findByRole('switch', { name: 'Enable scheduled restart' });
    await user.click(toggle);
    await act(async () => {
      vi.advanceTimersByTime(10000);
    });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Restart now' }).hasAttribute('disabled')).toBe(
        true,
      ),
    );
    expect(toggle.getAttribute('aria-checked')).toBe('true');
  });
  it('recovers the editor when polling succeeds after the initial request fails', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    vi.spyOn(HttpUtil, 'get')
      .mockResolvedValueOnce(new Msg(false, 'offline'))
      .mockResolvedValue(new Msg(true, '', VIEW));
    render(<RestartScheduleSection />);
    await screen.findByText('offline');
    await act(async () => {
      vi.advanceTimersByTime(10000);
    });
    const toggle = await screen.findByRole('switch', { name: 'Enable scheduled restart' });
    expect(toggle.getAttribute('aria-checked')).toBe('false');
    expect(screen.queryByText('offline')).toBeNull();
  });
  it('enables and saves a plan without immediately restarting Xray', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', VIEW));
    const post = vi
      .spyOn(HttpUtil, 'post')
      .mockResolvedValue(new Msg(true, '', { ...VIEW, config: { ...VIEW.config, enabled: true } }));
    const user = userEvent.setup();
    render(<RestartScheduleSection />);
    const toggle = await screen.findByRole('switch', { name: 'Enable scheduled restart' });
    await user.click(toggle);
    await user.click(screen.getByRole('button', { name: 'Save restart plan' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenCalledWith(
      '/panel/api/xray/schedule/restart',
      { ...VIEW.config, enabled: true },
      {
        silent: true,
        headers: { 'Content-Type': 'application/json' },
      },
    );
  });

  it('confirms an immediate restart and explains manual-stop skips', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', VIEW));
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(
      new Msg(true, '', {
        startedAt: Date.now(),
        durationMs: 0,
        status: 'skipped',
        trigger: 'manual',
        error: '',
      }),
    );
    const user = userEvent.setup();
    render(<RestartScheduleSection />);
    await user.click(await screen.findByRole('button', { name: 'Restart now' }));
    expect(post).not.toHaveBeenCalled();
    await user.click(await screen.findByRole('button', { name: 'OK' }));
    expect(await screen.findByText('Skipped because Xray was manually stopped.')).toBeTruthy();
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][0]).toBe('/panel/api/xray/schedule/restart/run');
  });
});
