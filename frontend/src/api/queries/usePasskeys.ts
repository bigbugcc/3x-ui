import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { keys } from '@/api/queryKeys';
import { passkeyRequest, passkeyRoot } from '@/api/passkey';
import {
  PasskeyConfigViewSchema,
  PasskeyListSchema,
  PasskeyEmptyResultSchema,
} from '@/schemas/passkey';

export function usePasskeys() {
  const queryClient = useQueryClient();
  const config = useQuery({
    queryKey: keys.passkeys.config(),
    queryFn: ({ signal }) =>
      passkeyRequest('GET', passkeyRoot + '/config', PasskeyConfigViewSchema, undefined, signal),
    retry: false,
  });
  const credentials = useQuery({
    queryKey: keys.passkeys.list(),
    queryFn: ({ signal }) =>
      passkeyRequest('GET', passkeyRoot, PasskeyListSchema, undefined, signal),
    retry: false,
  });
  const invalidateCredentials = () =>
    queryClient.invalidateQueries({ queryKey: keys.passkeys.list() });
  const rename = useMutation({
    mutationFn: ({ id, name }: { id: number; name: string }) =>
      passkeyRequest('POST', passkeyRoot + '/rename/' + id, PasskeyEmptyResultSchema, { name }),
    onSuccess: invalidateCredentials,
  });
  return { config, credentials, rename, invalidateCredentials };
}
