import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ConfigProvider } from 'antd';
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
  it('opens Geo file details in a modal without expanding the list or retaining the previous file', async () => {
    const firstHash = 'a'.repeat(64);
    const secondHash = 'b'.repeat(64);
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(
      new Msg(true, '', {
        ...VIEW,
        history: [
          {
            ...VIEW.history[1],
            details: 'geoFilesUpdated',
            hashComparisons: [
              { file: 'geoip.dat', status: 'baseline', currentHash: firstHash },
              { file: 'geosite.dat', status: 'changed', currentHash: secondHash },
            ],
          },
        ],
      }),
    );
    const { container } = render(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <ExecutionLogsSection />
      </ConfigProvider>,
    );
    const user = userEvent.setup();
    await user.click(await screen.findByText('geoip.dat'));
    const firstDialog = await screen.findByRole('dialog', { name: 'geoip.dat · Details' });
    expect(within(firstDialog).getByText(firstHash)).toBeTruthy();
    expect(
      within(firstDialog).getByText(
        'The first verified snapshot is saved for comparison with later runs.',
      ),
    ).toBeTruthy();
    expect(within(container).queryByText(firstHash)).toBeNull();
    await user.click(within(firstDialog).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await user.click(screen.getByRole('button', { name: 'geosite.dat' }));
    const secondDialog = await screen.findByRole('dialog', { name: 'geosite.dat · Details' });
    expect(within(secondDialog).getByText(secondHash)).toBeTruthy();
    expect(within(secondDialog).queryByText(firstHash)).toBeNull();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });
  it('summarizes Geo updates and lists each file once when steps also have comparisons', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(
      new Msg(true, '', {
        ...VIEW,
        history: [
          {
            ...VIEW.history[1],
            details: 'geoFilesUpdated',
            steps: [
              { target: 'geoip.dat', status: 'success', error: '' },
              { target: 'geosite.dat', status: 'success', error: '' },
            ],
            hashComparisons: [
              { file: 'geoip.dat', status: 'unchanged' },
              { file: 'geosite.dat', status: 'changed' },
            ],
          },
        ],
      }),
    );
    render(<ExecutionLogsSection />);
    expect(await screen.findByText('Geo files updated and reloaded.')).toBeTruthy();
    expect(screen.getAllByText('geoip.dat', { exact: false })).toHaveLength(1);
    expect(screen.getAllByText('geosite.dat', { exact: false })).toHaveLength(1);
    expect(screen.getByText('Content unchanged')).toBeTruthy();
    expect(screen.getByText('Content changed')).toBeTruthy();
  });

  it('folds the original Geo error without claiming every file download failed', async () => {
    const error = `scheduled geodata reload failed > download: ${'network error '.repeat(50)}`;
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(
      new Msg(true, '', {
        ...VIEW,
        history: [
          {
            ...VIEW.history[1],
            status: 'failed',
            details: '',
            error,
            steps: [{ target: 'geoip.dat', status: 'failed', error: '' }],
          },
        ],
      }),
    );
    render(<ExecutionLogsSection />);
    expect(await screen.findByText('Geo update failed.')).toBeTruthy();
    expect(screen.queryByText(error.trim())).toBeNull();
    expect(screen.queryByText('geoip.dat: Failed')).toBeNull();
    expect(screen.getAllByText('Failed')).toHaveLength(1);
    await userEvent.setup().click(screen.getByText('Original error'));
    expect(screen.getByText(error.trim()).textContent).toContain(error);
  });

  it.each([
    ['running', '', 'Updating Geo files; waiting for the execution result.'],
    ['skipped', 'geoNoDownloads', 'Update skipped: no download files configured.'],
    [
      'unconfirmed',
      'geoCoreStopped',
      'Xray stopped or restarted before the update result could be confirmed.',
    ],
    ['unconfirmed', 'geoResultUnavailable', 'The complete execution result is not available.'],
    ['success', 'geoReloadOnly', 'Local Geo data reloaded; no files were downloaded.'],
  ])('explains Geo %s execution details', async (status, details, summary) => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(
      new Msg(true, '', {
        ...VIEW,
        history: [{ ...VIEW.history[1], status, details }],
      }),
    );
    render(<ExecutionLogsSection />);
    expect(await screen.findByText(summary)).toBeTruthy();
  });
  it('renders execution and hash baseline times in the recorded task zone across midnight', async () => {
    const startedAt = Date.UTC(2026, 9, 17, 16, 15);
    const baselineAt = Date.UTC(2026, 9, 16, 17, 30);
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(
      new Msg(true, '', {
        ...VIEW,
        history: [
          {
            ...VIEW.history[1],
            startedAt,
            timezone: 'Asia/Shanghai',
            hashComparisons: [{ file: 'geosite.dat', status: 'unchanged', baselineAt }],
          },
        ],
      }),
    );
    render(<ExecutionLogsSection />);
    const execution = new Intl.DateTimeFormat('en-US', {
      dateStyle: 'medium',
      timeStyle: 'medium',
      timeZone: 'Asia/Shanghai',
    }).format(startedAt);
    expect(await screen.findByText(`${execution} (Asia/Shanghai)`)).toBeTruthy();
    await userEvent.setup().click(screen.getByText('geosite.dat'));
    const baseline = new Intl.DateTimeFormat('en-US', {
      dateStyle: 'medium',
      timeStyle: 'medium',
      timeZone: 'Asia/Shanghai',
    }).format(baselineAt);
    expect(screen.getByText(`Previous confirmation: ${baseline} (Asia/Shanghai)`)).toBeTruthy();
  });

  it('marks a legacy record displayed using the current task time zone', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(
      new Msg(true, '', {
        ...VIEW,
        history: [{ ...VIEW.history[0], timezone: 'Asia/Shanghai', timezoneInferred: true }],
      }),
    );
    render(<ExecutionLogsSection />);
    expect(
      await screen.findByText('Current plan time zone; original time zone was not recorded.'),
    ).toBeTruthy();
  });

  it('uses server offsets for Local executions and the baseline date across DST', async () => {
    const startedAt = Date.UTC(2026, 6, 1, 3, 15);
    const baselineAt = Date.UTC(2026, 0, 1, 3, 30);
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(
      new Msg(true, '', {
        ...VIEW,
        history: [
          {
            ...VIEW.history[1],
            startedAt,
            timezone: '-04:00',
            hashComparisons: [
              { file: 'geosite.dat', status: 'unchanged', baselineAt, baselineTimezone: '-05:00' },
            ],
          },
        ],
      }),
    );
    render(<ExecutionLogsSection />);
    const options = {
      dateStyle: 'medium',
      timeStyle: 'medium',
      timeZone: 'America/New_York',
    } as const;
    const execution = new Intl.DateTimeFormat('en-US', options).format(startedAt);
    expect(await screen.findByText(`${execution} (UTC-04:00)`)).toBeTruthy();
    await userEvent.setup().click(screen.getByText('geosite.dat'));
    const baseline = new Intl.DateTimeFormat('en-US', options).format(baselineAt);
    expect(screen.getByText(`Previous confirmation: ${baseline} (UTC-05:00)`)).toBeTruthy();
  });
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
    render(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <ExecutionLogsSection />
      </ConfigProvider>,
    );
    expect(await screen.findByText('Content changed')).toBeTruthy();
    expect(screen.getByText('Content unchanged')).toBeTruthy();
    expect(screen.getByText('First snapshot')).toBeTruthy();
    expect(screen.getByText('Unable to compare')).toBeTruthy();
    expect(screen.queryByText('permission denied')).toBeNull();
    expect(screen.getByText('Success')).toBeTruthy();
    const user = userEvent.setup();
    await user.click(screen.getByText('geosite.dat'));
    expect(await screen.findByText(previousHash)).toBeTruthy();
    expect(screen.getByText(currentHash)).toBeTruthy();
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await user.click(screen.getByText('geoip_custom.dat'));
    expect(screen.getByText('permission denied')).toBeTruthy();
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await user.click(screen.getByText('geosite_custom.dat'));
    expect(
      screen.getByText('The first verified snapshot is saved for comparison with later runs.'),
    ).toBeTruthy();
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
    expect(screen.getByText('Local Geo data reloaded; no files were downloaded.')).toBeTruthy();
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
