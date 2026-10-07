import { z } from 'zod';
import i18next from 'i18next';
import { HttpError } from '@/api/http-init';
import { HttpUtil } from '@/utils';

export const passkeyRoot = '/panel/api/setting/passkeys';
const errorSchema = z.object({
  msg: z.string().optional(),
  obj: z.object({ errors: z.record(z.string(), z.string()).optional() }).nullish(),
});

export class PasskeyRequestError extends Error {
  constructor(
    message: string,
    public fields: Record<string, string> = {},
  ) {
    super(message);
  }
}

export async function passkeyRequest<T extends z.ZodType>(
  method: 'GET' | 'POST',
  path: string,
  schema: T,
  body?: unknown,
  signal?: AbortSignal,
): Promise<z.infer<T>> {
  try {
    const options = {
      headers: { 'Content-Type': 'application/json' },
      silent: true,
      throwOnError: true,
      signal,
    };
    const msg = await (method === 'GET'
      ? HttpUtil.get(path, undefined, options)
      : HttpUtil.post(path, body, options));
    if (!msg.success)
      throw new PasskeyRequestError(msg.msg || i18next.t('passkey.errors.unavailable'));
    const result = schema.safeParse(msg.obj);
    if (!result.success) throw new PasskeyRequestError(i18next.t('passkey.errors.unavailable'));
    return result.data;
  } catch (error) {
    if (error instanceof HttpError) {
      const data = errorSchema.safeParse(error.response.data);
      throw new PasskeyRequestError(
        (data.success && data.data.msg) || i18next.t('passkey.errors.unavailable'),
        data.success ? data.data.obj?.errors : undefined,
      );
    }
    throw error;
  }
}
