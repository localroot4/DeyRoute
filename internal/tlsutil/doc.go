// Package tlsutil holds every piece of X.509/TLS machinery deyroute needs
// (spec sections 3, 4, 7.2, 10 and 11):
//
//   - the internal CA (Ed25519, 20 years) created during hub setup, and the
//     certificates it issues: the hub control certificate (ECDSA P-256,
//     ServerAuth), node client certificates signed from a CSR during join
//     (10 years, ClientAuth, CN forced by the hub) and one tunnel TLS
//     certificate per tunnel (ECDSA P-256, SAN = hub IP(s) + domain, 3 years,
//     renewed 30 days before expiry, yellow warning 14 days before);
//   - CA fingerprints ("sha256:<64 hex>") and CA pinning for the join step;
//   - tls.Config builders for the mTLS control channel (TLS 1.3 only,
//     ALPN "deyroute/1");
//   - certificate inspection, renewal checks and validation of owner-supplied
//     ("custom") certificates;
//   - a PKCS#12 encoder (rathole/tls needs a .p12; no openssl is used);
//   - atomic 0600 secret files and the secrets permission audit (DEY-S002);
//   - ACME (Let's Encrypt) issuance through lego with HTTP-01 or Cloudflare
//     DNS-01.
//
// Every error an owner can see is a *errors.Error with a DEY-T/N/S/X code.
// The package never runs external programs.
package tlsutil
