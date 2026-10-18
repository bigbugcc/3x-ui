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
  outboundTags: ['warp', 'nord', 'pia'],
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('restart schedule', () => {
  it('saves typed hours and minutes without selecting dropdown options', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', VIEW));
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', VIEW));
    const user = userEvent.setup();
    render(<RestartScheduleSection />);
    const hour = await screen.findByRole('spinbutton', { name: 'Hour' });
    const minute = screen.getByRole('spinbutton', { name: 'Minute' });
    await user.clear(hour);
    await user.type(hour, '9');
    await user.clear(minute);
    await user.type(minute, '5');
    await user.click(screen.getByRole('button', { name: 'Save restart plan' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post.mock.calls[0]?.[1]).toEqual({
      ...VIEW.config,
      cron: '5 9 * * 0',
    });
  });
  it('saves selected VPN outbound restarts and retains them in the editor', async () => {
    const config = { ...VIEW.config, restartOutbounds: ['warp'] };
    let saved = false;
    const savedConfig = { ...config, restartOutbounds: ['warp', 'nord'] };
    vi.spyOn(HttpUtil, 'get').mockImplementation(
      async () => new Msg(true, '', { ...VIEW, config: saved ? savedConfig : config }),
    );
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async () => {
      saved = true;
      return new Msg(true, '', { ...VIEW, config: savedConfig });
    });
    const user = userEvent.setup();
    render(<RestartScheduleSection />);
    await user.click(await screen.findByText('Advanced: additional outbound restarts'));
    const warp = await screen.findByRole('checkbox', { name: 'warp' });
    expect((warp as HTMLInputElement).checked).toBe(true);
    await user.click(screen.getByRole('checkbox', { name: 'nord' }));
    await user.click(screen.getByRole('button', { name: 'Save restart plan' }));
    await screen.findByText('Plan saved');
    expect((screen.getByRole('checkbox', { name: 'nord' }) as HTMLInputElement).checked).toBe(true);
    expect(post.mock.calls[0]?.[1]).toEqual({ ...config, restartOutbounds: ['warp', 'nord'] });
  });
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
