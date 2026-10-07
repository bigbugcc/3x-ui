# Passkey Setup and Deployment

English | [简体中文](passkey.md)

Passkey login is disabled by default. Configure it under **Panel Settings → Security → Passkeys**.

Keep a working administrator password. Adding or revoking a credential, or saving the login configuration, requires the current password and a verification code if TOTP is enabled. Passkey login requires device user verification, such as a fingerprint, face recognition, or PIN, without entering a username, password, or TOTP code. Password login continues to follow the existing TOTP settings.

## Enable Passkey Login

1. Open the panel through its external HTTPS domain, for example `https://panel.example.com:8443/secret/`.
2. Set the site domain to `panel.example.com` and the allowed origin to `https://panel.example.com:8443`. Do not include `/secret/`, use an IP address, wildcard, parent-domain scope, or HTTP. Enter one origin per line, up to 16 entries. Every origin must use the same exact hostname.
3. Select the HTTPS deployment mode. Direct mode requires a configured certificate and private key that the panel can load, and an actual TLS connection from the browser to the panel. Reverse-proxy mode requires the proxy to preserve the original Host header and the trusted proxy addresses to be configured.
4. Turn on the feature and click **Check configuration**. This checks the configuration format and the current origin; it does not test a real device, certificate, or proxy deployment.
5. Click **Save Passkey configuration**, review the impact, and enter your password/TOTP code. A successful save invalidates all previous browser sessions. Sign in again with your password. Passkey configuration takes effect immediately and does not require the global save action in another settings tab.
6. Click **Add Passkey**, enter a recognizable device name, reauthenticate, and complete the browser or system verification prompt. Add a backup device if possible. Each account can have up to 10 credentials.
7. Sign out, then click **Sign in with a passkey** on the login page and select a registered credential. Use password login if the browser does not support Passkeys or the current origin is not allowed.

## Reverse Proxy

**Trusted proxy addresses** uses the existing shared Web setting. It affects client IP resolution, login rate limits, and HTTPS cookies. The default is `127.0.0.1/32,::1/128`. Enter the actual proxy addresses or controlled network ranges. IPv4, IPv6, and CIDR notation are supported; `0.0.0.0/0`, `::/0`, and an empty list in reverse-proxy mode are rejected.

The following directives illustrate the proxy configuration and should be merged into your existing Nginx configuration. The proxy must overwrite client-supplied forwarding headers, and the backend port should accept connections only from the proxy.

```nginx
location /secret/ {
    proxy_pass http://127.0.0.1:2053;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
}
```

`X-Forwarded-Proto: https` is accepted only from trusted proxies. Preserve an external non-default port in both the Host header and the allowed origin. For multiple proxy layers, configure trust and client IP forwarding for the actual topology rather than trusting headers supplied directly by clients.

## Revocation and Recovery

- Renaming affects only the display name. Revocation requires password/TOTP reauthentication and invalidates all browser sessions for that account.
- Disabling the feature retains credentials but blocks registration and login. Changing the site domain retains old-domain records for viewing and deletion; credentials must be registered again on the new domain.
- Changing or resetting the administrator password revokes all Passkeys for that account. Server-side revocation does not remove entries from the system password manager; remove those separately if needed.
- Local recovery commands: `x-ui reset-passkeys` through the management script, or `/usr/local/x-ui/x-ui setting -resetPasskeys=true` through the binary. They target the first administrator in the database and use the existing database environment configuration.
- Resetting TOTP through the CLI invalidates previous sessions and pending authorizations while retaining Passkey credentials.

A full backup contains the Passkey configuration, user mappings, and credential public-key data. It does not contain device private keys or temporary challenges. A full restore assigns fresh random session epochs and configuration versions. Restoring an old backup can bring back credentials that were revoked later. If compromise is suspected, revoke all Passkeys locally after restoration.

An import with **Keep local settings** retains the local Passkey configuration and proxy settings but deletes imported user mappings and credentials. This prevents reused user IDs in different databases from transferring credentials to another account. Register credentials again after the import.

Before replacing the database, the panel creates a `<database-name>.db.auth-restore` recovery journal in the database directory and suspends new panel requests. Service resumes and the journal is removed only after authentication state has been refreshed successfully. If an import fails or the process is interrupted, restarting the panel processes authentication recovery from the journal before opening HTTP service. If recovery fails, HTTP service remains unavailable. Preserve a damaged journal and repair the database or restore the journal; do not bypass protection by simply deleting it. An interrupted import with **Keep local settings** still clears credentials, so sign in with your password and register them again. The service account must be able to create, sync, and delete the journal in that directory.

## Verification and Limitations

The current implementation supports one panel instance. Challenges are held in bounded memory, expire after 120 seconds, and are consumed atomically. Restart an operation after the panel restarts. Management authorizations are single-use and valid for five minutes. Canceling a device prompt usually does not constitute a server-side verification failure; unfinished challenges expire automatically.

LDAP is the password authentication fallback for existing local accounts. Passkeys are bound to local accounts and do not query LDAP status on each login. Keep this feature disabled if directory account deactivation must immediately block login.

Development verification:

```powershell
npm --prefix frontend run build
$env:XUI_PASSKEY_BROWSER_TEST = '1'
go test ./internal/web/controller -run '^TestPasskeyBrowser$' -count=1 -v
```

Browser verification requires an installed Playwright Chromium and uses a separate temporary database without modifying the running panel. Desktop and mobile screenshots are saved to `.cache/passkey-browser/`. Local signature and virtual-authenticator checks have passed; they do not establish acceptance for real devices, trusted production proxies, or a real PostgreSQL deployment.
