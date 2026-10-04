import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import GeodataSection from '@/pages/index/GeodataSection';
import { HttpUtil, Msg } from '@/utils';
import { keys } from '@/api/queryKeys';
import { makeTestQueryClient, renderWithProviders as render } from './test-utils';

const CUSTOM_SOURCE = { url: 'https://example.com/geosite_custom.dat', file: 'geosite_custom.dat' };
const STANDARD_SOURCES = [
  {
    url: 'https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat',
    file: 'geoip.dat',
  },
  {
    url: 'https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat',
    file: 'geosite.dat',
  },
  {
    url: 'https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geoip.dat',
    file: 'geoip_IR.dat',
  },
  {
    url: 'https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geosite.dat',
    file: 'geosite_IR.dat',
  },
  {
    url: 'https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geoip.dat',
    file: 'geoip_RU.dat',
  },
  {
    url: 'https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geosite.dat',
    file: 'geosite_RU.dat',
  },
];
const VIEW = {
  config: {
    enabled: false,
    cron: '0 4 * * *',
    timezone: 'UTC',
    outbound: '',
    assets: [CUSTOM_SOURCE],
  },
  nextRun: 0,
  standardSources: STANDARD_SOURCES,
  outboundTags: ['direct'],
  applied: false,
  applyError: '',
};

afterEach(() => vi.restoreAllMocks());

describe('GeodataSection', () => {
  it('preserves edited assets when the server query is refreshed', async () => {
    vi.spyOn(HttpUtil, 'get')
      .mockResolvedValueOnce(new Msg(true, '', VIEW))
      .mockResolvedValue(new Msg(true, '', { ...VIEW, nextRun: 100000 }));
    const queryClient = makeTestQueryClient();
    const user = userEvent.setup();
    render(<GeodataSection active />, { queryClient });
    const url = await screen.findByDisplayValue(CUSTOM_SOURCE.url);
    await user.clear(url);
    await user.type(url, 'https://example.com/edited.dat');
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: keys.xray.schedule.geodata() });
    });
    expect(screen.getByDisplayValue('https://example.com/edited.dat')).toBeTruthy();
    expect(screen.queryByDisplayValue(CUSTOM_SOURCE.url)).toBeNull();
  });
  it('prevents saving Geo while another Xray editor has unsaved template changes', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', VIEW));
    const post = vi.spyOn(HttpUtil, 'post');
    const user = userEvent.setup();
    render(<GeodataSection active saveDisabled />);
    await screen.findByDisplayValue(CUSTOM_SOURCE.url);
    const save = screen.getByRole('button', { name: 'Save Geo plan' });
    expect(save.hasAttribute('disabled')).toBe(true);
    await user.click(save);
    expect(screen.queryByRole('button', { name: 'Confirm' })).toBeNull();
    expect(post).not.toHaveBeenCalled();
  });
  it('adds standard sources without removing custom sources or enabling the plan', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', VIEW));
    const user = userEvent.setup();
    render(<GeodataSection active />);
    await screen.findByDisplayValue(CUSTOM_SOURCE.url);
    await user.click(screen.getByRole('button', { name: 'Use standard sources' }));
    for (const source of STANDARD_SOURCES) {
      expect(screen.getByDisplayValue(source.url)).toBeTruthy();
      expect(screen.getByDisplayValue(source.file)).toBeTruthy();
    }
    expect(
      screen
        .getByRole('switch', { name: 'Enable scheduled Geo updates' })
        .getAttribute('aria-checked'),
    ).toBe('false');
  });

  it('saves only the Geo plan as JSON and does not request another restart', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', VIEW));
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', VIEW));
    const user = userEvent.setup();
    render(<GeodataSection active />);
    await screen.findByDisplayValue(CUSTOM_SOURCE.url);
    await user.click(screen.getByRole('button', { name: 'Save Geo plan' }));
    await user.click(await screen.findByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenCalledWith('/panel/api/xray/schedule/geodata', VIEW.config, {
      silent: true,
      headers: { 'Content-Type': 'application/json' },
    });
    expect(
      await screen.findByText('Plan saved. It will be applied when Xray next starts.'),
    ).toBeTruthy();
  });

  it('keeps application errors visible after the server saves the configuration', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', VIEW));
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(
      new Msg(true, '', { ...VIEW, applyError: 'failed to bind port' }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<GeodataSection active onClose={onClose} />);
    await screen.findByDisplayValue(CUSTOM_SOURCE.url);
    await user.click(screen.getByRole('button', { name: 'Save Geo plan' }));
    await user.click(await screen.findByRole('button', { name: 'Confirm' }));
    await waitFor(() =>
      expect(
        screen.getAllByText('Plan saved, but applying it failed: failed to bind port').length,
      ).toBeGreaterThan(0),
    );
    expect(onClose).not.toHaveBeenCalled();
  });

  it('shows a failed load and allows retry instead of leaving an indefinite spinner', async () => {
    const get = vi
      .spyOn(HttpUtil, 'get')
      .mockResolvedValueOnce(new Msg(false, 'offline'))
      .mockResolvedValue(new Msg(true, '', VIEW));
    const user = userEvent.setup();
    render(<GeodataSection active />);
    await screen.findByText('offline');
    await user.click(screen.getByRole('button', { name: 'Check' }));
    await screen.findByDisplayValue(CUSTOM_SOURCE.url);
    expect(get).toHaveBeenCalledTimes(2);
  });
});
