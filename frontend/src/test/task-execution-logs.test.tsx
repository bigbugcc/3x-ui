import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ExecutionLogsSection from '@/pages/xray/schedule/ExecutionLogsSection';
import { HttpUtil, Msg } from '@/utils';
import { keys } from '@/api/queryKeys';
import { makeTestQueryClient, renderWithProviders as render } from './test-utils';

const VIEW = {
  settings: { retentionDays: 30 },
  history: [
    {
      id: 'restart-test',
      startedAt: Date.now(),
      durationMs: 50,
      trigger: 'scheduled',
      status: 'success',
      error: '',
      task: 'restart',
      details: '',
      steps: [{ target: 'warp', status: 'success', error: '' }],
    },
    {
      id: 'geo-test',
      startedAt: Date.now() - 1000,
      durationMs: 12,
      trigger: 'scheduled',
      status: 'success',
      error: '',
      task: 'geodata',
      details: 'geoReloadOnly',
    },
  ],
};
afterEach(() => vi.restoreAllMocks());

describe('task execution logs', () => {
  it('does not restore cleared logs when an earlier refresh completes late', async () => {
    let resolveRefresh!: (value: Msg) => void;
    const refresh = new Promise<Msg>((resolve) => {
      resolveRefresh = resolve;
    });
    const get = vi
      .spyOn(HttpUtil, 'get')
      .mockResolvedValueOnce(new Msg(true, '', VIEW))
      .mockReturnValue(refresh);
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', { ...VIEW, history: [] }));
    const queryClient = makeTestQueryClient();
    const user = userEvent.setup();
    render(<ExecutionLogsSection />, { queryClient });
    expect(await screen.findByText('warp: Success')).toBeTruthy();
    const refetch = queryClient.refetchQueries({ queryKey: keys.xray.schedule.logs() });
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
    await user.click(screen.getByRole('button', { name: 'Clear logs' }));
    await user.click(await screen.findByRole('button', { name: 'OK' }));
    await waitFor(() => expect(screen.queryByText('warp: Success')).toBeNull());
    await act(async () => {
      resolveRefresh(new Msg(true, '', VIEW));
      await refetch;
    });
    expect(queryClient.getQueryData(keys.xray.schedule.logs())).toEqual({ ...VIEW, history: [] });
    expect(screen.queryByText('warp: Success')).toBeNull();
  });
  it('shows per-file SHA-256 comparisons separately from execution success', async () => {
    const previousHash = 'b'.repeat(64);
    const currentHash = 'c'.repeat(64);
    const comparisons = [
      {
        file: 'geosite.dat',
        status: 'changed',
        previousHash,
        currentHash,
        baselineAt: 1700000000000,
      },
      { file: 'geoip.dat', status: 'unchanged', previousHash, currentHash: previousHash },
      { file: 'geosite_custom.dat', status: 'baseline', currentHash },
      { file: 'geoip_custom.dat', status: 'unavailable', error: 'permission denied' },
    ];
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(
      new Msg(true, '', {
        ...VIEW,
        history: [{ ...VIEW.history[1], details: 'geoFilesUpdated', hashComparisons: comparisons }],
      }),
    );
    render(<ExecutionLogsSection />);
    expect(await screen.findByText('Content changed')).toBeTruthy();
    expect(screen.getByText('Content unchanged')).toBeTruthy();
    expect(screen.getByText('Baseline established')).toBeTruthy();
    expect(screen.getByText('Comparison unavailable')).toBeTruthy();
    expect(screen.getByText('permission denied')).toBeTruthy();
    expect(screen.getByText('Success')).toBeTruthy();
    const user = userEvent.setup();
    await user.click(screen.getByText('geosite.dat'));
    expect(await screen.findByText(previousHash)).toBeTruthy();
    expect(screen.getByText(currentHash)).toBeTruthy();
  });
  it('combines restart and Geo results and confirms clearing before removing them', async () => {
    let cleared = false;
    vi.spyOn(HttpUtil, 'get').mockImplementation(
      async () => new Msg(true, '', cleared ? { ...VIEW, history: [] } : VIEW),
    );
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async () => {
      cleared = true;
      return new Msg(true, '', { ...VIEW, history: [] });
    });
    const user = userEvent.setup();
    render(<ExecutionLogsSection />);
    expect(await screen.findByText('warp: Success')).toBeTruthy();
    expect(
      screen.getByText(
        'Existing Geo databases reloaded successfully; no download assets are configured.',
      ),
    ).toBeTruthy();
    expect(screen.getByText('12 ms')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Clear logs' }));
    expect(post).not.toHaveBeenCalled();
    await user.click(await screen.findByRole('button', { name: 'OK' }));
    await waitFor(() => expect(screen.queryByText('warp: Success')).toBeNull());
    expect(screen.queryByText('12 ms')).toBeNull();
  });

  it('saves retention and shows the saved value', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', VIEW));
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(
      new Msg(true, '', { ...VIEW, settings: { retentionDays: 7 } }),
    );
    const user = userEvent.setup();
    render(<ExecutionLogsSection />);
    const input = await screen.findByRole('spinbutton', { name: 'Retention (days)' });
    await waitFor(() => expect(input.hasAttribute('disabled')).toBe(false));
    await user.clear(input);
    await user.type(input, '7');
    await user.click(screen.getByRole('button', { name: 'Save log settings' }));
    expect(await screen.findByText('Log settings saved')).toBeTruthy();
    expect((input as HTMLInputElement).value).toBe('7');
  });
});
