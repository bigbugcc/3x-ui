# Passkey Login Requirements and Technical Design

English | [简体中文](passkey-login-requirements.md)

Status: implementation plan v0.3. The first release has been implemented; acceptance testing on real devices and a PostgreSQL deployment remains pending. Date: 2026-10-04.
v0.3 implements a dedicated configuration UI, username-free login, credential management, and recovery revocation. Backend signature tests and the Chromium virtual-authenticator flow have passed. For operating instructions, see [Passkey Setup and Deployment](../passkey.en.md).
Initial investigation baseline: `d498688a` on `feat/xray-scheduled-tasks`. The current implementation is based on main (`8f00e780`) on the independent `feat/passkey-login` branch.

## 1. Goals and Recommended Decisions

Provide Passkey login for 3x-ui panel administrators. Users authenticate with a device fingerprint, face recognition, PIN, or a security key supporting user verification, reducing the need to enter passwords and verification codes.

In this document, “must” identifies an acceptance requirement. The first release follows these decisions:

| Decision                               | First-release recommendation                                                                                           | Reason                                                                    |
| -------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| Relationship between Passkeys and TOTP | Independent Passkey login with device user verification; password login continues to follow the existing TOTP settings | Avoid requiring a separate verification code for every Passkey login      |
| Retain passwords                       | Retain them; disabling password login is not supported yet                                                             | Provide recovery when a device is lost or the domain changes              |
| Enter a username                       | Not required; use discoverable credentials                                                                             | The system selects the account and credential after the button is clicked |
| Supported accounts                     | Existing panel administrator accounts                                                                                  | No new signup flow or proxy-client authentication                         |
| First-release interaction              | Explicitly click “Sign in with a passkey”                                                                              | Autofill and conditional UI are deferred                                  |

Independent Passkey login is a product policy, not a claim that every WebAuthn credential can replace a password and TOTP. The first release accepts only discoverable credentials with user verification. Since password login remains available, the account cannot be described as having eliminated all password-related risks.

If TOTP is ever required after Passkey verification, design a separate, short-lived pending-login state with no panel access. Establish a full session only after TOTP verification; do not log the user in first and rely on the frontend to collect a code afterward.

## 2. Existing System and Integration Points

These findings come from the repository's code, not assumptions about an upstream release.

| Module                                                                          | Existing behavior                                                                                                        | Design impact                                                                                           |
| ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------- |
| `internal/web/controller/index.go`                                              | `/login` handles form validation, rate limiting, notifications, and session persistence; exposes an anonymous CSRF token | Add Passkey endpoints and reuse login completion                                                        |
| `internal/web/service/panel/user.go`                                            | Finds a local user; may try LDAP if the local password fails; then verifies global TOTP                                  | Resolve the local user through the credential rather than the password-validation branch                |
| `internal/database/model/model.go`                                              | User contains Id, Username, Password, and LoginEpoch                                                                     | Add WebAuthn user mappings and credential models                                                        |
| `internal/web/session/session.go`                                               | Cookies store the user ID and LoginEpoch; identity checks load the user and verify the epoch                             | Reuse the same session and invalidation mechanism                                                       |
| `internal/web/web.go`                                                           | Gin, signed cookie sessions, configurable basePath; direct HTTPS originally determined the Secure cookie setting         | Keep ceremony state server-side and support external HTTPS reverse proxies                              |
| `internal/web/middleware/security.go`                                           | CSRF, CSP, and related protections; API identities may bypass CSRF                                                       | Passkey management must require browser sessions and must not bypass protection through an API identity |
| `internal/web/controller/login_limiter.go`                                      | Bounded IP + username rate limiter: five failures within five minutes trigger a 15-minute cooldown                       | Username-free login needs additional IP limits and bounded challenge storage                            |
| `frontend/src/pages/login/LoginPage.tsx`                                        | React password/TOTP login through HttpUtil                                                                               | Add an independent button and browser credential calls                                                  |
| `frontend/src/pages/settings/SecurityTab.tsx`                                   | Security settings, account/password changes, and TOTP management                                                         | Add a Passkeys sub-tab for deployment configuration and credential management                           |
| `frontend/src/pages/settings/SettingsPage.tsx`, `api/queries/useAllSettings.ts` | Shared save action for all settings                                                                                      | Save Passkey configuration independently; general saves must not overwrite sensitive configuration      |
| `internal/database/db.go`, `migrate_data.go`                                    | GORM model registration, SQLite/PostgreSQL, migration and export                                                         | Include the new models in both database backends and full backups                                       |
| `main.go`                                                                       | Password updates and TOTP resets                                                                                         | Add a server-local Passkey revocation recovery command                                                  |

Existing account credential changes require the old username, old password, and TOTP when enabled. A recent Passkey login does not automatically replace verification for every sensitive operation.

LDAP currently serves as a password fallback for existing local panel users. First-release Passkeys bind only to that local user and do not query LDAP on each login. Disabling an LDAP account therefore does not automatically invalidate local Passkeys. Keep Passkeys disabled where directory lifecycle enforcement is required; real-time LDAP integration is a separate requirement.

## 3. First-Release Scope

### Required Features

- A deployment-level switch, disabled by default; existing login must remain available after an upgrade.
- A panel configuration UI for the switch, RP ID, allowed origins, HTTPS mode, and trusted proxies, with validation and independent saving. Users must not need to edit the database.
- A “Sign in with a passkey” button for username-free login.
- Viewing, adding, renaming, and deleting the current user's Passkeys under security settings.
- Up to 10 active credentials per user, supporting platform authenticators, synced Passkeys, and security keys with discoverable credentials and user verification.
- Names of 1–64 characters, trimmed at both ends and rendered as text.
- Name, creation time, and last-used time; show “Not used yet” when appropriate. Users supply device names; the panel does not promise to identify the device model or sync provider.
- Server-side verification, rate limiting, login auditing, lost-device recovery, and SQLite/PostgreSQL migration.
- Instructions for custom basePath, direct HTTPS, and trusted HTTPS reverse proxies.

### Out of Scope

- Disabling passwords or requiring every administrator to use Passkeys.
- Signup, invitations, multiple permission roles, or proxy-client Passkeys.
- Autofill/conditional mediation, cross-site iframes, or Related Origin Requests across domains.
- Enterprise attestation policies, vendor allowlists, or a custom QR-based cross-device authentication protocol.
- A new recovery-code system, LDAP status synchronization, or shared challenge storage for multiple instances.

Cross-device authentication may be provided by the browser or operating system. Acceptance depends on supported platforms; the panel does not generate a separate pairing QR code.

## 4. User Flows

### 4.1 Enable the Feature

Administrators configure the Passkey switch, RP ID, and allowed external origins in security settings. Saving or disabling the feature requires the current account password and TOTP when enabled. Initial enablement must also pass external-origin validation.

For `https://panel.example.com:8443/secret/`, the RP ID is `panel.example.com` and the origin is `https://panel.example.com:8443`. The basePath is part of neither value.

Before enablement, explain domain binding, retaining a recovery password, and adding a backup device. Never automatically generate and save trusted configuration from an anonymous request's Host header.

### 4.1.1 Configuration UI Location and Layout

Add a Passkeys sub-tab inside **Settings → Security**, following Ant Design, SettingListItem, existing themes, and mobile styles. The tab remains accessible when the feature is disabled so that users can configure it.

Display, in order:

1. Feature status: disabled, enabled, or invalid configuration. Show current-browser capability separately from deployment status.
2. Login configuration: feature switch, site domain, allowed origins, HTTPS mode, and necessary proxy settings.
3. Actions: “Use current origin”, “Check configuration”, “Save Passkey configuration”, and “Discard changes”.
4. “My Passkeys”: credential list and add button. Disabled deployments still allow viewing, renaming, and deleting credentials; disable addition with an explanation.

Configuration applies to the whole panel; the credential list belongs only to the current account. Load them independently. A failed configuration request must not display default values as if they had been saved; provide a retry action.

### 4.1.2 Configuration Fields

| UI label                  | API field                  | Control and default                                              | Validation / interaction                                                                                                                                           |
| ------------------------- | -------------------------- | ---------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Allow Passkey login       | `config.enabled`           | Switch, disabled by default                                      | Changes only the draft until saving succeeds                                                                                                                       |
| Site domain (RP ID)       | `config.rpId`              | Input, initially empty                                           | No scheme, port, or path; exact hostname of the allowed origins; example `panel.example.com`                                                                       |
| Allowed origins           | `config.origins`           | Multiline text, one origin per line, initially empty             | Full external HTTPS origins; deduplicate; reject wildcards, IPs, user info, paths, queries, and fragments; at least one when enabled                               |
| HTTPS deployment mode     | `config.httpsMode`         | Select, direct mode by default; HTTPS reverse proxy as an option | Direct mode requires a loadable certificate/key and actual TLS ceremony requests; proxy mode accepts HTTPS forwarding headers only from trusted sources            |
| Trusted proxy addresses   | `config.trustedProxyCIDRs` | Reuse existing `trustedProxyCIDRs`, loopback by default          | Required in proxy mode; IPv4/IPv6 or CIDR; reject ranges covering the entire network; explain the shared effect on password-login IP resolution and Secure cookies |
| Login verification policy | Confirmed product policy   | Read-only explanation                                            | Explain Passkey/TOTP behavior and password recovery; no runtime policy switch in this release                                                                      |

Use a dedicated singleton `PasskeyConfig` table with Version and JSON Data updated atomically in one row. Proxy addresses remain in the shared Web setting `trustedProxyCIDRs`. The configuration endpoint saves both and invalidates sessions in the same transaction. General settings reject any `passkey*` field; once dedicated configuration exists, proxy changes must also go through this reauthenticated endpoint.

Provide Chinese field explanations without requiring users to understand RP ID or Origin terminology beforehand. An allowed-origin example is `https://panel.example.com:8443`; do not include the site's subpath.

“Use current origin” fills only a draft from the current page's `window.location.hostname/origin` and identifies the source. Users must review and save it. Explain why HTTP or IP addresses cannot be used in production instead of automatically trusting them. Do not overwrite edits silently; show a replacement preview when a draft already exists.

### 4.1.3 Validation, Saving, and Activation

- “Check configuration” performs frontend and read-only server validation, returning field errors, normalized configuration, and whether the current origin would remain allowed. It creates no credentials, writes no configuration, and does not fetch arbitrary user-supplied external URLs.
- The page can check browser capability, secure context, and origin matching. State the limits of checks for TLS termination, proxy trust, and actual devices; configuration-text validation is not a successful Passkey login test.
- Show a change summary before saving. Explain that disabling blocks Passkey login, changing RP ID requires registration on the new domain, and removing the current origin prevents Passkey use from that address.
- “Save Passkey configuration” prompts for the current password and enabled TOTP. The backend validates every field again and updates configuration/version atomically. Failed verification or invalid fields must leave everything unchanged.
- Submit the configuration version captured when loading. If it has changed, return 409 and ask the user to reload or review the difference. Never silently overwrite another tab's changes.
- Empty RP ID/origins are allowed when the feature is disabled and unconfigured. Clearing an active RP configuration must also disable the feature. Validate partial configuration rather than saving contradictory values.
- Prevent repeated submissions while saving. Canceling verification retains the draft; failed saves show the reason and preserve input; discarding restores the last saved values.
- Refresh configuration and status after success. If a security change invalidates the session, explain that saving succeeded and return to login; expected logout must not be reported as a save failure.
- The switch and RP/origin configuration take effect immediately after saving. If proxy trust or cookie changes genuinely require a restart, return `restartRequired` and show a pending state with the existing restart action. Do not present a pending change as active.

Passkey configuration has its own draft and save endpoint, outside the global “Save all settings” payload. General updates must return a recognizable error for explicitly submitted Passkey fields, preventing the UI or direct API requests from bypassing reauthentication. Other shared-proxy mutation paths need equivalent protection.

### 4.2 Add a Passkey

1. Sign in to the panel and open **Settings → Security → Passkeys**.
2. Enter a name and the current password, plus a TOTP code when enabled. Obtain username/user ID from the current session.
3. Successful server verification produces a short-lived authorization for addition only, then registration options and a ceremony ID.
4. Call `navigator.credentials.create()`; the system verifies the user and creates a credential.
5. Consume the ceremony and verify the response server-side; recheck the user and configuration version within the transaction before saving.
6. Show success and refresh the list only after server persistence succeeds.

Both the first and subsequent credentials require reauthentication. Use the same password + existing TOTP policy for management; reauthentication with an existing Passkey is not an alternative in this release. Possession of a cookie session alone is insufficient to add a lasting login credential.

If device creation succeeds but server persistence fails, explain that panel registration did not complete, allow retry, and note that an orphaned credential may need manual removal from the device.

### 4.3 Passkey Login

1. Open the login page and click “Sign in with a passkey” without entering a username or password.
2. Return an anonymous login challenge; do not disclose administrator usernames, account counts, or the site's credential list.
3. Call `navigator.credentials.get()`; the system selects an account and performs user verification.
4. Verify the signature and resolve ownership jointly through RP ID, credential ID, and userHandle. Confirm that the account exists and the credential remains valid.
5. Persist authenticator state and last-used time, establish the existing panel session, and redirect to `basePath + panel/`.

Return success and issue success notifications only after all verification, database writes, and session persistence succeed. Do not log a successful login before saving the session.

Keep the password form available. Its required fields and TOTP validation must not block Passkey actions. Cancellation or timeout returns the UI to an actionable state without clearing entered password-form values.

### 4.4 Management and Lost-Device Recovery

- Listing and renaming require a logged-in browser session. Renaming also requires CSRF, without another password prompt.
- Deletion requires the session, CSRF, and password/TOTP authorization for that specific target. It revokes the panel's acceptance of the credential; it cannot guarantee removal of private keys from devices or password managers.
- Allow deletion of the last Passkey because password login remains available, and state that explicitly in confirmation.
- Increment the user's LoginEpoch after deletion, invalidating all previous browser sessions, including the current one, then return to login. Per-device session revocation is out of scope.
- Use password + TOTP login to delete a lost device's credential; use server-local recovery if the password is forgotten.
- The implemented CLI `x-ui setting -resetPasskeys=true` and management command `x-ui reset-passkeys` revoke all Passkeys for the first administrator and increment LoginEpoch. Old management authorizations and registration ceremonies cannot continue. Do not print credential material.

## 5. Deployment and Account Lifecycle

### 5.1 Domains, HTTPS, and Proxies

WebAuthn requires a secure context. Production requires a trusted HTTPS domain; bare public IP addresses and ordinary HTTP are unsupported. Domain constraints follow the [W3C WebAuthn specification](https://www.w3.org/TR/webauthn-3/#sctn-createCredential); secure-context requirements are described in the [MDN API documentation](https://developer.mozilla.org/en-US/docs/Web/API/Web_Authentication_API). Development verification also uses `https://localhost` and a virtual authenticator without an ordinary-HTTP exception.

See section 4.1 for configuration fields. Normalize origins to scheme + host + non-default port, without paths, queries, or fragments. Reject wildcards, arbitrary parent-domain scope, and arbitrary origin reflection. RP ID is the panel's exact hostname. Multiple origins may specify ports on that same hostname, all using HTTPS. Normalize internationalized domains with IDNA and omit the default port 443.

Treat the proxy's external HTTPS address separately from the backend HTTP address. Configure explicit external origins and ensure both login and anonymous-ceremony cookies have Secure, HttpOnly, and appropriate SameSite/Path attributes. Accept forwarding headers only from configured trusted proxies; direct backend callers must not be able to spoof verification inputs or the rate-limit IP.

The frontend checks `window.isSecureContext`, PublicKeyCredential, and the credentials API; the backend independently validates configuration and signed origins. Failed platform-authenticator detection does not prove security keys are unavailable and must not hide every Passkey entry point.

### 5.2 Change Behavior

| Event                                                                   | Required behavior                                                                                                                                                                         |
| ----------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Disable Passkey login                                                   | Block registration/login and invalidate pending ceremonies; retain credentials for reenablement; increment user LoginEpoch                                                                |
| Change RP ID                                                            | Explain that old credentials cannot work for the new RP; retain old-RP records for viewing/deletion; login matches only the current RP; clear ceremonies and invalidate previous sessions |
| Change allowed origins                                                  | Require reauthentication; increment the version, clear old ceremonies, and invalidate sessions; adding a valid port for the same RP does not require registering credentials again        |
| Change HTTPS mode or trusted proxies                                    | Save after reauthentication; clear ceremonies and invalidate sessions; show immediate or pending-restart activation accurately                                                            |
| Change basePath                                                         | Keep credentials bound to the same RP; clear ceremonies, sign in again, and verify the cookie path                                                                                        |
| Change the account password through the API or reset it through the CLI | Revoke all Passkeys for that account, increment LoginEpoch, and require registration again; explain this in the UI and CLI                                                                |
| A future username-only update                                           | Keep userHandle stable rather than recreating credentials after a display-name change; the current update endpoint also requires a new password                                           |
| Change TOTP configuration                                               | Reuse epoch invalidation and discard old authorizations/ceremonies; retain valid Passkeys under the independent-login policy                                                              |
| Delete an account                                                       | Delete its WebAuthn mappings and credentials; do not transfer them to a new account                                                                                                       |
| Restart the panel                                                       | Retain persisted credentials; invalidate in-memory ceremonies so the user starts a new operation                                                                                          |

Revoking all credentials during password recovery is a security policy: changing a password must not leave a lost device able to enter the panel. Both UpdateUser and UpdateFirstUser must implement this behavior. When updating the session, UpdateUser must use the user snapshot returned by the same update transaction. Do not use the pre-increment epoch or reload after commit and issue a newer epoch that may belong to concurrent recovery.

### 5.3 Backup, Import, and Rollback

Full database backups must include user mappings, credentials, and RP configuration. Challenges and short-lived authorizations are not backed up. Configuration templates, node settings, and other partial migrations must not carry account credentials.

After a full restore, credentials for the same RP and user mapping can continue to work; the server still does not obtain private keys. Old backups may revive later-revoked credentials and roll back counters. Explain this in the restore flow and documentation; revoke all credentials locally if compromise is suspected.

Restore must rotate the browser-session signing secret or provide equivalent invalidation that an old backup cannot roll back, and invalidate ceremonies. The backup's LoginEpoch alone is insufficient. The current implementation assigns fresh random session epochs and configuration versions, writes a durable recovery journal before replacing the database, and suspends new requests. Protection remains active if authentication recovery fails; restart recovers authentication state before opening the panel. See [Passkey Setup and Deployment](../passkey.en.md) for the journal and failure handling.

Schema changes are additive and do not remove password fields. Password login remains available after reverting to an older program. Do not assume an older program's export preserves unfamiliar tables; retain a full backup before rollback.

## 6. Security and Server Design

These parameters are project recommendations, not mandatory values from the standard. Fix them before implementation and verify them with tests.

- Registration requires `residentKey=required`, `userVerification=required`, and `attestation=none`; do not restrict `authenticatorAttachment`, allowing platform and cross-platform devices.
- Login requires `userVerification=required`; verify UP/UV, challenge, type, Origin, RP ID hash, signature, and ownership server-side. Reject cross-site embedding.
- Use challenges with at least 32 random bytes and unpredictable ceremony IDs. TTL is 120 seconds; the UI explains that timed-out operations can be retried.
- Keep ceremony state server-side; cookies carry only an opaque session-binding identifier. Putting complete WebAuthn SessionData in replayable signed cookies does not make it single-use.
- Consume atomically. Duplicate finish requests, cross-session submissions, and registration challenges submitted as login must not succeed. Verification failure also invalidates the ceremony.
- Bind state to the anonymous browser session, purpose, and origin/RP configuration version. Management ceremonies also bind user ID, LoginEpoch, and the target operation.
- Management authorization is recommended to expire after five minutes, be single-use, and bind a specific action. Registration begin consumes authorization; finish still checks account/configuration state.
- Do not trust caller-supplied user ID, username, or userHandle as identity. Resolve identity through stored records and signature verification, with no fallback to the first administrator.
- New management routes accept real browser cookie sessions rather than Bearer/mTLS API identities. Every mutation checks CSRF.
- When saving races with login or deletion, the final transaction rechecks credential revocation, user epoch, and configuration version, preventing a deleted credential from regaining access.
- Logout, recovery, and security configuration changes invalidate relevant pending state. Successful login rotates CSRF/anonymous binding instead of inheriting unauthenticated state.

Recommended anonymous challenge limits are 20 per IP per minute, three active ceremonies per anonymous session, and 10,000 per process. Reclaim expired entries and return rate-limit/busy responses at capacity rather than growing without bounds. Finish has a separate IP failure limit and joins the existing account limiter once a user is identified. Switching between password and Passkey entry points must not bypass account limits; Passkey success must not clear anonymous challenge-flood counters.

Device cancellation usually happens only in the frontend and is not recorded as server-side signature failure. Invalid, expired, replayed, and incorrectly signed server submissions remain rate-limited. Anonymous responses must not distinguish an unknown credential from an existing account.

Not all authenticators increment signature counters; do not require every counter to be positive. W3C treats anomalous counters as a risk signal. This project recommends accepting zero counters normally, rejecting and auditing rollback/repeated nonzero counters for non-backup credentials, and recording library-reported risks for backup credentials without permanently disabling them solely because of counter rollback. Verify this with synced credentials and concurrent logins. See the [specification's counter guidance](https://www.w3.org/TR/webauthn-3/#sctn-sign-counter).

The first release uses locked, TTL-based in-memory ceremony storage for a single panel instance without Redis. Restart affects only in-progress ceremonies. Future multi-instance support needs shared storage with atomic consumption; occasional success behind a round-robin proxy is not evidence of support.

## 7. Data Models and Library Choice

The implementation uses `github.com/go-webauthn/webauthn v0.18.2` for protocol verification and Go 1.27.1. Dependencies/checksums are in go.mod/go.sum and have passed local compilation and signature verification. Protocol requirements follow [W3C WebAuthn Level 3](https://www.w3.org/TR/webauthn-3/).

The library supports `BeginDiscoverableLogin` and `FinishPasskeyLogin`/`ValidatePasskeyLogin`, and requires correct persistence of authenticator state. See the [library API and storage documentation](https://pkg.go.dev/github.com/go-webauthn/webauthn/webauthn).

| New model         | Core fields and constraints                                                                                                 |
| ----------------- | --------------------------------------------------------------------------------------------------------------------------- |
| PasskeyConfig     | Singleton id=1, version, and JSON data; transactionally saved with shared trusted proxies                                   |
| WebAuthnUser      | id, user_id, rp_id, and a stable random 32-byte user_handle; unique `(rp_id,user_id)` and `(rp_id,user_handle)`             |
| PasskeyCredential | id, user_id, rp_id, credential_id, name, complete credential_data, created_at, last_used_at; unique `(rp_id,credential_id)` |

Use canonical unpadded base64url text for indexed credential IDs and userHandle values to avoid database binary-type differences; validate canonical encoding during conversion. Names and last-used times are panel metadata, not substitutes for protocol state.

Store complete credential_data in a versioned serialization format containing the library credential object: public key, AAGUID, transports, attestation information, flags, signature counter, backup state, clone warning, and extension data. Persist the library's updated object after login, not just last-used time; require round-trip tests on both SQLite and PostgreSQL.

userHandle remains stable for the lifetime of the account within an RP and is shared by all credentials. Do not use a mutable username or generate a new handle on every registration. Database uniqueness guarantees one mapping during concurrent first registrations. Keep the handle when all credentials are revoked but the account remains.

Persistence must prevent reassignment of a credential to another user. Deletion must filter by current user and record ID. Lists return only the management ID, name, times, and necessary status; never public keys, complete credential_data, or internal userHandle.

Wrap native browser WebAuthn APIs and base64url conversions directly. Prefer native JSON conversion where available, with a tested fallback, so basic login does not depend on new Level 3 interfaces. Consider a frontend helper library only if implementation complexity warrants it.

## 8. API Endpoints

Paths are relative to the existing basePath and use the `{success,msg,obj}` response envelope. These routes have been implemented and included in generated OpenAPI documentation.

| Method and path                                    | Identity requirements                                    | Purpose                                                                                 |
| -------------------------------------------------- | -------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| GET `/passkey/status`                              | Anonymous                                                | Report whether deployment is enabled/available without account or credential statistics |
| POST `/passkey/login/begin`                        | Anonymous session + CSRF + IP rate limit                 | Return ceremonyId and publicKey options                                                 |
| POST `/passkey/login/finish`                       | Same anonymous session + CSRF                            | Consume ceremonyId/assertion and complete login                                         |
| GET `/panel/api/setting/passkeys/config`           | Browser session                                          | Return saved configuration, version, and effective status                               |
| POST `/panel/api/setting/passkeys/config/validate` | Browser session + CSRF                                   | Validate a draft read-only and return field errors/impact information                   |
| POST `/panel/api/setting/passkeys/config`          | Browser session + CSRF + current password/TOTP           | Save atomically and return activation/restart/reauthentication requirements             |
| GET `/panel/api/setting/passkeys`                  | Browser session                                          | List the current user's credentials                                                     |
| POST `/panel/api/setting/passkeys/reauth`          | Browser session + CSRF                                   | Verify password/TOTP and issue action-bound single-use authorization                    |
| POST `/panel/api/setting/passkeys/register/begin`  | Browser session + CSRF + addition authorization          | Return registration ceremonyId and publicKey options                                    |
| POST `/panel/api/setting/passkeys/register/finish` | Same browser session + CSRF                              | Verify and save a new credential                                                        |
| POST `/panel/api/setting/passkeys/rename/:id`      | Browser session + CSRF                                   | Update the name                                                                         |
| POST `/panel/api/setting/passkeys/delete/:id`      | Browser session + CSRF + matching deletion authorization | Revoke the credential and invalidate previous sessions                                  |

Management keeps SettingController's `/panel/api/setting` prefix but adds browser-only protection rather than inheriting Bearer/mTLS acceptance. The recommended configuration payload is `{config,expectedVersion,currentPassword,twoFactorCode}`. Passwords/codes are used only for verification, never stored in the configuration table, returned, or logged.

Read/save responses distinguish persisted/effective state, version, and restartRequired/reauthRequired. Use stable field paths for validation errors. Return key impact information with the successful save before session invalidation takes effect. Reauthentication must not issue an unlimited authorization for arbitrary management actions.

Begin returns `obj={ceremonyId,publicKey}`; finish accepts `{ceremonyId,credential}`. Validate the registration name at begin and bind it in server state. Finish cannot change ownership or arbitrarily replace the name.

Recommended statuses: 400 invalid input, 401 unauthenticated, 403 CSRF/origin violation, 409 duplicate/version conflict, 410 expired, 429 rate-limited, and 503 unavailable. Keep authentication failure messages generic and align the final error representation with HttpUtil; do not rely solely on HTTP 200 or string matching.

Options, authentication responses, and lists use `Cache-Control: no-store`. Passkey endpoints have a separate recommended body limit of 64 KiB, returning 413 when exceeded. Verify actual response sizes for attestation=none.

## 9. UI, Auditing, and Error Handling

Determine login-button visibility/availability only from deployment state and browser capability, with an explanation when unavailable. A failed status request must not leave password login loading indefinitely. Give actionable guidance for unsupported browsers, ordinary HTTP, no matching credentials, cancellation, and timeout.

NotAllowedError may mean cancellation, timeout, or no available credential. When indistinguishable, say “Passkey verification was not completed; retry or use your password” rather than inventing a definite cause. Support keyboard interaction, focus restoration, loading states, duplicate-submit prevention, and AbortController cancellation when switching login methods.

Audit events include login method, user ID after successful verification, trusted client IP, time, outcome, and server reason code. Audit additions, deletions, and deployment changes. Successful login uses existing Telegram notifications, marks the Passkey method, and respects existing notification settings. Never log passwords, TOTP, complete authentication responses, public-key material, or recovery authorizations.

Revocation does not mean a sync provider has removed the credential. Explain that users may need to remove the system password-manager entry. Use existing i18n for new frontend/backend messages, with complete Chinese/English strings, project-standard fallback for other languages, and missing-key checks.

## 10. Implementation Stages and Completion Criteria

| Stage                         | Deliverables                                                                                               | Completion criteria                                                                                              |
| ----------------------------- | ---------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| A: requirements               | Login/recovery policy, deployment constraints, API, and models                                             | Both core product decisions settled and lifecycle behavior testable                                              |
| B: backend foundation         | Pinned dependencies, three-table migration, RP validation, bounded single-use ceremonies, reauthentication | SQLite/PostgreSQL, replay, cross-use, and concurrency tests pass                                                 |
| C: authentication integration | Registration/login, shared completion flow, rate limits, auditing, and sessions                            | Both methods share invalidation behavior; failed persistence never reports success                               |
| D: configuration and frontend | Security sub-tab, form/validation/independent save, login button, list, device flow, and translations      | Configuration/error/reauthentication/concurrent-save tests pass; types/build and virtual-authenticator flow pass |
| E: operations                 | CLI revocation, password-change integration, full backup/restore, and deployment docs                      | Acceptance records cover real devices, proxies, and databases                                                    |

Suggested backend locations are `controller/passkey.go`, `service/panel/passkey.go`, models, and a separate ceremony store. Frontend locations include `utils/passkey.ts`, `pages/settings/PasskeySection.tsx`, and `PasskeyConfigForm.tsx`, integrated into SecurityTab. Maintain dedicated queries/saves, schemas, and generated types rather than borrowing global saveAll. Filenames may evolve while responsibility boundaries remain.

Password and Passkey login share a function that reports success only after persisting the session, retaining password-error behavior. Do not disguise Passkey verification as successful password checking. Add tables to allModels and existing migration/export/restore registries; keep API documentation and generated types synchronized.

## 11. Acceptance Checklist

| ID   | Scenario                                                                  | Expected result                                                                                                                |
| ---- | ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| AC01 | Upgrade an old database; feature disabled by default                      | Password/TOTP login works; new tables are added incrementally                                                                  |
| AC02 | Add with correct/incorrect password and enabled TOTP                      | Only correct verification can start registration; failure issues no usable authorization                                       |
| AC03 | Register platform/synced/discoverable UV-capable security-key credentials | Show registration only after server success; do not downgrade to missing UV or non-discoverable credentials                    |
| AC04 | Username-free login with TOTP enabled/disabled                            | Establish the session under the agreed independent-login policy without TOTP; password login still requires enabled TOTP       |
| AC05 | Successful login versus failed session persistence                        | Success accesses the panel; persistence failure neither returns nor notifies success                                           |
| AC06 | Missing CSRF, wrong Origin/RP/type/signature, unknown userHandle          | Reject without a session or account disclosure                                                                                 |
| AC07 | Expiry, replay, cross-session/cross-purpose submission, concurrent finish | At most one success; expired and mismatched submissions fail                                                                   |
| AC08 | Multiple tabs, cancellation, timeout, status-request failure              | No mixed-up state; password login stays actionable                                                                             |
| AC09 | Another user's credential, API-token/mTLS management                      | No ownership bypass or replacement of browser sessions/reauthentication                                                        |
| AC10 | Delete the last credential; deletion racing login; restart                | Password recovery works; revoked credentials fail and old sessions expire; persisted credentials survive restart               |
| AC11 | Password changes through CLI/UI, CLI revocation, TOTP changes             | Apply credential/session/authorization invalidation across every lifecycle path                                                |
| AC12 | SQLite/PostgreSQL upgrade, full export/restore                            | Restore fields completely; same-RP login works; explain old-backup risk and invalidate previous sessions                       |
| AC13 | Custom basePath, non-default HTTPS port, Nginx/Caddy TLS termination      | Correct RP/origin and cookie attributes/path; spoofed proxy headers are ineffective                                            |
| AC14 | HTTP/IP/unsupported browser, domain change                                | Explain limits, retain password login, and require registration for a new RP                                                   |
| AC15 | Flood begin, fail finish, switch login methods                            | Bound memory and enforce limits; normal success does not clear flood counters                                                  |
| AC16 | Zero counters, synced backups, non-backup rollback, concurrent login      | Apply the risk policy without banning all synced credentials                                                                   |
| AC17 | Duplicate registration or concurrent attempts at the 10-credential limit  | No reassignment, duplicates, or excess credentials; understandable transaction-conflict errors                                 |
| AC18 | Open configuration while disabled                                         | All fields remain viewable/editable; enable through the UI without database edits                                              |
| AC19 | Invalid domain/origin/proxy CIDR; check and save                          | Accurate field errors; read-only checks; backend rejects invalid configuration without partial writes                          |
| AC20 | Save/disable/change RP; cancel or use wrong password/TOTP                 | Require correct reauthentication; cancellation/failure preserves draft and saved values; show an impact summary                |
| AC21 | Submit Passkey fields through global save/general API                     | No bypass of dedicated configuration/reauthentication or overwrite of saved policy                                             |
| AC22 | Concurrent saves, reload, restart/logout requirements                     | Explicit stale-version conflict; success matches effective state; persisted values remain correct after reauthentication       |
| AC23 | Disabled configuration, failed loading, mobile, Chinese/English           | Existing credentials remain manageable when disabled; retry failures without fabricated defaults; working layouts/translations |

Test layers: backend protocol/routes, frontend interactions, virtual-authenticator end-to-end, and real Windows Hello, Apple platforms, and at least one synced Passkey/UV-capable security key. Record actual browser/OS versions for Chrome/Edge, Safari, and Firefox. Untested platforms must not be claimed as accepted.

Local verification covers real ECDSA signatures; rejection of wrong origins, missing UV, and invalid signatures; single-use challenges/authorizations; cookie/CSRF/API identity isolation; counter policies; password-reset revocation; version conflicts; random restore invalidation; configuration rollback; and frontend loading/cancel/retry. Chromium's virtual authenticator uses the real frontend/controllers, temporary SQLite, and HTTPS to exercise configuration save, confirmation cancellation, registration, independent login, rename, and revocation. Desktop/mobile screenshots have been inspected.

Real Windows Hello, Apple/synced password managers, security keys, trusted TLS proxies, and PostgreSQL require acceptance in their own environments. The virtual authenticator ignores the test server's self-signed certificate and does not prove production certificate configuration. Chinese and English Passkey strings are complete; other locales currently use the full English fallback.

For code review, architectural cleanup, fixes, and latest local validation, see [Passkey Code Review and Cleanup (Chinese)](passkey-code-review.md).

For the high-risk authentication audit and restore protection, see [Passkey Security Review (Chinese)](passkey-security-review.md).
