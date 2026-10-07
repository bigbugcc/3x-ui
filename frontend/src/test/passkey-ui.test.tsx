import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import PasskeySection from '@/pages/settings/PasskeySection';
import PasskeyLogin from '@/pages/login/PasskeyLogin';
import { passkeyRequest } from '@/api/passkey';
import { passkeySupported } from '@/utils/passkey';
import type { PasskeyConfigView } from '@/schemas/passkey';

vi.mock('@/utils/passkey', async (original) => ({
  ...(await original<typeof import('@/utils/passkey')>()),
  passkeySupported: vi.fn(() => false),
}));
vi.mock('@/api/passkey', async (original) => ({
  ...(await original<typeof import('@/api/passkey')>()),
  passkeyRequest: vi.fn(),
}));
function renderSettings() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const result = render(<PasskeySection />, {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  });
  return { ...result, client };
}
afterEach(() => {
  vi.mocked(passkeyRequest).mockReset();
  vi.mocked(passkeySupported).mockReturnValue(false);
  vi.unstubAllGlobals();
});
const saved: PasskeyConfigView = {
  config: {
    enabled: false,
    rpId: '',
    origins: [],
    httpsMode: 'direct',
    trustedProxyCIDRs: '127.0.0.1/32,::1/128',
  },
  version: 0,
  twoFactorEnabled: false,
  validationErrors: {},
  currentOriginAllowed: false,
};

describe('Passkey settings', () => {
  it('keeps the original optimistic version when a background refresh meets local edits', async () => {
    let serverView = saved;
    vi.mocked(passkeyRequest).mockImplementation(async (method, path, _schema, body) => {
      if (method === 'GET') return path.endsWith('/config') ? serverView : [];
      if (path.endsWith('/validate'))
        return {
          config: (body as { config: unknown }).config,
          errors: {},
          currentOriginAllowed: false,
        };
      throw new Error('Expected configuration conflict');
    });
    const { client } = renderSettings();
    const input = await screen.findByLabelText('Site domain (RP ID)');
    fireEvent.change(input, { target: { value: 'my-draft.example.com' } });
    serverView = {
      ...saved,
      version: 2,
      config: { ...saved.config, rpId: 'someone-else.example.com' },
    };
    await act(async () => client.invalidateQueries({ queryKey: ['passkeys', 'config'] }));
    expect((input as HTMLInputElement).value).toBe('my-draft.example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Save Passkey configuration' }));
    fireEvent.change(await screen.findByLabelText('Current Password'), {
      target: { value: 'secret' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'OK' }));
    await waitFor(() =>
      expect(
        vi
          .mocked(passkeyRequest)
          .mock.calls.some(
            ([method, path, _schema, body]) =>
              method === 'POST' &&
              path.endsWith('/config') &&
              (body as { expectedVersion: number }).expectedVersion === 0,
          ),
      ).toBe(true),
    );
  });
  it('does not display editable defaults until configuration has loaded', async () => {
    let resolve!: (value: PasskeyConfigView) => void;
    vi.mocked(passkeyRequest).mockImplementation((_, path) =>
      path.endsWith('/config')
        ? new Promise<PasskeyConfigView>((done) => {
            resolve = done;
          })
        : Promise.resolve([]),
    );
    renderSettings();
    expect(screen.queryByLabelText('Site domain (RP ID)')).toBeNull();
    await waitFor(() => expect(resolve).toBeDefined());
    await act(async () => resolve(saved));
    expect(await screen.findByLabelText('Site domain (RP ID)')).toBeTruthy();
    expect(
      (screen.getByRole('button', { name: 'Add Passkey' }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it('keeps the draft and saved state when configuration review is canceled', async () => {
    vi.mocked(passkeyRequest).mockImplementation(async (method, path, _schema, body) => {
      if (method === 'GET') return path.endsWith('/config') ? saved : [];
      if (path.endsWith('/validate'))
        return {
          config: (body as { config: unknown }).config,
          errors: {},
          currentOriginAllowed: false,
        };
      throw new Error('Unexpected mutation: ' + path);
    });
    renderSettings();
    const input = await screen.findByLabelText('Site domain (RP ID)');
    fireEvent.change(input, { target: { value: 'panel.example.com' } });
    fireEvent.change(screen.getByLabelText('Allowed access addresses'), {
      target: { value: 'https://panel.example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save Passkey configuration' }));
    expect(await screen.findByRole('dialog')).toBeTruthy();
    const password = screen.getByLabelText('Current Password') as HTMLInputElement;
    fireEvent.change(password, { target: { value: 'temporary-secret' } });
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect((input as HTMLInputElement).value).toBe('panel.example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Save Passkey configuration' }));
    await waitFor(() =>
      expect((screen.getByLabelText('Current Password') as HTMLInputElement).value).toBe(''),
    );
    expect(
      vi
        .mocked(passkeyRequest)
        .mock.calls.filter(([method, path]) => method === 'POST' && !path.endsWith('/validate')),
    ).toHaveLength(0);
  });

  it('offers retry and never substitutes defaults after a configuration error', async () => {
    vi.mocked(passkeyRequest).mockImplementation(async (_, path) => {
      if (path.endsWith('/config')) throw new Error('Cannot load configuration');
      return [];
    });
    renderSettings();
    expect(await screen.findByText('Cannot load configuration')).toBeTruthy();
    expect(screen.queryByLabelText('Site domain (RP ID)')).toBeNull();
    vi.mocked(passkeyRequest).mockImplementation(async (_, path) =>
      path.endsWith('/config') ? saved : [],
    );
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByLabelText('Site domain (RP ID)')).toBeTruthy();
  });
});

it('Passkey login runs with empty password fields and cancellation creates no session request', async () => {
  vi.mocked(passkeySupported).mockReturnValue(true);
  vi.mocked(passkeyRequest).mockImplementation(async (method) =>
    method === 'GET'
      ? { enabled: true, available: true }
      : { ceremonyId: 'test', publicKey: { challenge: 'Af4', userVerification: 'required' } },
  );
  const get = vi.fn().mockRejectedValue(new DOMException('Canceled', 'NotAllowedError'));
  vi.stubGlobal('navigator', { credentials: { get } });
  const onBusy = vi.fn();
  render(
    <>
      <input aria-label="Username" />
      <input aria-label="Password" />
      <PasskeyLogin passwordBusy={false} onBusyChange={onBusy} />
    </>,
  );
  fireEvent.click(await screen.findByRole('button', { name: /Sign in with a Passkey/ }));
  await waitFor(() => expect(get).toHaveBeenCalledOnce());
  await waitFor(() => expect(onBusy).toHaveBeenLastCalledWith(false));
  expect((screen.getByLabelText('Username') as HTMLInputElement).value).toBe('');
  expect((screen.getByLabelText('Password') as HTMLInputElement).value).toBe('');
  expect(vi.mocked(passkeyRequest).mock.calls.some(([, path]) => path.endsWith('/finish'))).toBe(
    false,
  );
});
