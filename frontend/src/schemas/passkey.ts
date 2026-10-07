import { z } from 'zod';
import {
  PasskeySettingsSchema,
  PasskeyConfigViewSchema as GeneratedConfigViewSchema,
  PasskeyCredentialSchema,
} from '@/generated/zod';

export const PasskeyConfigSchema = PasskeySettingsSchema.extend({
  httpsMode: z.enum(['direct', 'proxy']),
});
export const PasskeyConfigViewSchema = GeneratedConfigViewSchema.extend({
  config: PasskeyConfigSchema,
  version: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
  twoFactorEnabled: z.boolean(),
  validationErrors: z.record(z.string(), z.string()),
  currentOriginAllowed: z.boolean(),
});
export const PasskeyValidationSchema = z.object({
  config: PasskeyConfigSchema,
  errors: z.record(z.string(), z.string()),
  currentOriginAllowed: z.boolean(),
});
export const PasskeyListSchema = z.array(PasskeyCredentialSchema);
export const PasskeyStatusSchema = z.object({ enabled: z.boolean(), available: z.boolean() });
export const PasskeyAuthorizationSchema = z.object({ authorizationId: z.string().min(1) });
export const PasskeySessionResultSchema = z.object({ reauthRequired: z.boolean() });
export const PasskeyEmptyResultSchema = z.null();
export const PasskeyNameSchema = z
  .string()
  .trim()
  .refine((value) => Array.from(value).length >= 1 && Array.from(value).length <= 64, {
    message: 'passkey.errors.name',
  });

const binary = z
  .string()
  .min(1)
  .regex(/^[A-Za-z0-9_-]+$/);
const verification = z.enum(['required', 'preferred', 'discouraged']);
const descriptor = z.object({
  type: z.literal('public-key'),
  id: binary,
  transports: z.array(z.enum(['ble', 'hybrid', 'internal', 'nfc', 'usb'])).optional(),
});
export const CreationOptionsSchema = z.object({
  challenge: binary,
  rp: z.object({ name: z.string(), id: z.string().optional() }),
  user: z.object({ id: binary, name: z.string(), displayName: z.string() }),
  pubKeyCredParams: z.array(z.object({ type: z.literal('public-key'), alg: z.number().int() })),
  timeout: z.number().optional(),
  excludeCredentials: z.array(descriptor).optional(),
  authenticatorSelection: z
    .object({
      authenticatorAttachment: z.enum(['platform', 'cross-platform']).optional(),
      residentKey: z.enum(['required', 'preferred', 'discouraged']).optional(),
      requireResidentKey: z.boolean().optional(),
      userVerification: verification.optional(),
    })
    .optional(),
  attestation: z.enum(['none', 'indirect', 'direct', 'enterprise']).optional(),
  extensions: z.object({ credProps: z.boolean().optional() }).optional(),
  hints: z.array(z.enum(['security-key', 'client-device', 'hybrid'])).optional(),
});
export const RequestOptionsSchema = z.object({
  challenge: binary,
  rpId: z.string().optional(),
  timeout: z.number().optional(),
  allowCredentials: z.array(descriptor).optional(),
  userVerification: verification.optional(),
});
export const PasskeyRegistrationSchema = z.object({
  ceremonyId: z.string().min(1),
  publicKey: CreationOptionsSchema,
});
export const PasskeyLoginSchema = z.object({
  ceremonyId: z.string().min(1),
  publicKey: RequestOptionsSchema,
});

export type PasskeyConfig = z.infer<typeof PasskeyConfigSchema>;
export type PasskeyConfigView = z.infer<typeof PasskeyConfigViewSchema>;
export type PasskeyRow = z.infer<typeof PasskeyCredentialSchema>;
export type CreationOptionsJSON = z.infer<typeof CreationOptionsSchema>;
export type RequestOptionsJSON = z.infer<typeof RequestOptionsSchema>;
export type PasskeyStatus = z.infer<typeof PasskeyStatusSchema>;
