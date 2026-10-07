import { describe, expect, it, vi } from 'vitest';
import { HttpError, httpRequest } from '@/api/http-init';
import { passkeyRequest, PasskeyRequestError } from '@/api/passkey';
import { PasskeyConfigViewSchema, PasskeyStatusSchema, PasskeyNameSchema } from '@/schemas/passkey';
import {
  creationOptions,
  requestOptions,
  serializeCredential,
  encodeBase64url,
  decodeBase64url,
} from '@/utils/passkey';

vi.mock('@/api/http-init', async (original) => ({
  ...(await original<typeof import('@/api/http-init')>()),
  httpRequest: vi.fn(),
}));

describe('Passkey browser protocol adapter', () => {
  it('roundtrips binary including url-unsafe and zero bytes without padding', () => {
    const bytes = Uint8Array.from([0, 255, 254, 127, 128, 1]);
    const encoded = encodeBase64url(bytes.buffer);
    expect(encoded).not.toMatch(/[+/=]/);
    expect(new Uint8Array(decodeBase64url(encoded))).toEqual(bytes);
  });

  it('decodes creation and assertion options while retaining the server policy', () => {
    const creation = creationOptions({
      rp: { name: '3x-ui', id: 'panel.example.com' },
      user: { name: 'admin', displayName: 'admin', id: 'AP8' },
      challenge: 'Af4',
      pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
      authenticatorSelection: { residentKey: 'required', userVerification: 'required' },
      excludeCredentials: [{ type: 'public-key', id: 'AgM', transports: ['internal'] }],
    });
    expect(new Uint8Array(creation.challenge as ArrayBuffer)).toEqual(Uint8Array.from([1, 254]));
    expect(new Uint8Array(creation.user.id as ArrayBuffer)).toEqual(Uint8Array.from([0, 255]));
    expect(creation.authenticatorSelection).toEqual({
      residentKey: 'required',
      userVerification: 'required',
    });
    expect(creation.excludeCredentials?.[0].transports).toEqual(['internal']);
    const request = requestOptions({
      challenge: 'Af4',
      rpId: 'panel.example.com',
      userVerification: 'required',
    });
    expect(request.allowCredentials).toBeUndefined();
    expect(request.userVerification).toBe('required');
  });

  it('serializes registration transports and extensions and nullable assertion handles', () => {
    const buffer = Uint8Array.from([0, 255]).buffer;
    const base = {
      id: 'AP8',
      rawId: buffer,
      type: 'public-key',
      authenticatorAttachment: 'platform',
      getClientExtensionResults: () => ({ credProps: { rk: true } }),
    };
    const registration = serializeCredential({
      ...base,
      response: {
        clientDataJSON: buffer,
        attestationObject: buffer,
        getTransports: () => ['internal', 'hybrid'],
      },
    } as unknown as PublicKeyCredential);
    expect(registration.response).toEqual({
      clientDataJSON: 'AP8',
      attestationObject: 'AP8',
      transports: ['internal', 'hybrid'],
    });
    expect(registration.clientExtensionResults).toEqual({ credProps: { rk: true } });
    const assertion = serializeCredential({
      ...base,
      response: {
        clientDataJSON: buffer,
        authenticatorData: buffer,
        signature: buffer,
        userHandle: null,
      },
    } as unknown as PublicKeyCredential);
    expect(assertion.response).toEqual({
      clientDataJSON: 'AP8',
      authenticatorData: 'AP8',
      signature: 'AP8',
      userHandle: null,
    });
  });

  it('uses the shared CSRF client with JSON and preserves server field errors', async () => {
    vi.mocked(httpRequest).mockRejectedValueOnce(
      new HttpError(400, 'Bad Request', {
        msg: 'Invalid configuration',
        obj: { errors: { rpId: 'passkey.errors.rpId' } },
      }),
    );
    const signal = new AbortController().signal;
    const failed = passkeyRequest(
      'POST',
      '/panel/api/setting/passkeys/config',
      PasskeyConfigViewSchema,
      { config: {} },
      signal,
    );
    await expect(failed).rejects.toBeInstanceOf(PasskeyRequestError);
    await expect(failed).rejects.toMatchObject({ fields: { rpId: 'passkey.errors.rpId' } });
    expect(httpRequest).toHaveBeenCalledWith(
      'POST',
      '/panel/api/setting/passkeys/config',
      { config: {} },
      { headers: { 'Content-Type': 'application/json' }, signal },
    );
  });

  it('rejects malformed server data before it reaches a settings or login view', async () => {
    vi.mocked(httpRequest).mockResolvedValueOnce({
      ok: true,
      status: 200,
      statusText: 'OK',
      data: { success: true, obj: { enabled: 'false', available: true } },
    });
    await expect(
      passkeyRequest('GET', '/passkey/status', PasskeyStatusSchema),
    ).rejects.toBeInstanceOf(PasskeyRequestError);
  });

  it('counts credential names by Unicode code points, consistently with the server', () => {
    expect(PasskeyNameSchema.safeParse('🔑'.repeat(64)).success).toBe(true);
    expect(PasskeyNameSchema.safeParse('🔑'.repeat(65)).success).toBe(false);
    expect(PasskeyNameSchema.safeParse('  ').success).toBe(false);
  });
});
